// Command document-service-spacectl is the administrative CLI for knowledge
// spaces, membership and QQ group bindings.
//
// It replaces the pre-ADR-017 `gin-backend` spacectl, which wrote the space and
// binding tables directly. Space, membership and binding are document service
// business objects now, so the administrator goes through the same authenticated
// boundary every other caller uses: a signed service assertion carrying the
// space.admin scope. Nothing here touches a database.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	documentv1 "packages/gen/document/v1"
	"packages/serviceauth"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "spacectl:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("spacectl", flag.ContinueOnError)
	endpoint := flags.String("endpoint", "127.0.0.1:18081", "document service gRPC address")
	keyPath := flags.String("capability-key-file", os.Getenv("DOCUMENT_SERVICE_CAPABILITY_KEY_FILE"),
		"shared boundary key file")
	callerName := flags.String("caller", "spacectl", "service identity presented to the document service")
	subjectKey := flags.String("subject", "", "resource subject the command acts for, e.g. web:user:42 (required)")
	actor := flags.String("actor", "", "operator identity recorded in audit rows (required)")
	reason := flags.String("reason", "", "change reason recorded in audit rows (required)")
	timeout := flags.Duration("timeout", 15*time.Second, "per-call timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	rest := flags.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: spacectl [flags] create-team|bind-group|revoke-group|add-member|revoke-member|list-subjects|get-space [args]")
	}

	actorValue := strings.TrimSpace(*actor)
	reasonValue := strings.TrimSpace(*reason)
	if actorValue == "" || reasonValue == "" {
		// Every mutation is audited with who did it and why. Accepting a blank
		// value would produce audit rows that answer neither question.
		return fmt.Errorf("-actor and -reason are required")
	}
	// Every command acts for a subject. The document service decides what that
	// subject may reach, and CreateTeamSpace records it as the owner, so an
	// unnamed operator would leave the team space without an accountable owner.
	subjectValue := strings.TrimSpace(*subjectKey)
	if subjectValue == "" {
		return fmt.Errorf("-subject is required (for example web:user:42)")
	}
	caller := serviceauth.Caller(strings.TrimSpace(*callerName))
	if !caller.Valid() {
		return fmt.Errorf("caller %q is not a registered service identity", *callerName)
	}
	key, err := readKey(*keyPath)
	if err != nil {
		return err
	}
	codec, err := serviceauth.NewCodec(key)
	if err != nil {
		return err
	}
	conn, err := grpc.NewClient(*endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial %s: %w", *endpoint, err)
	}
	defer func() { _ = conn.Close() }()
	client := documentv1.NewDocumentServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	ctx, err = withAssertion(ctx, codec, caller, subjectValue, actorValue, rest[0])
	if err != nil {
		return err
	}

	switch rest[0] {
	case "create-team":
		return createTeam(ctx, client, rest[1:], subjectValue, actorValue, reasonValue)
	case "bind-group":
		return bindGroup(ctx, client, rest[1:], actorValue, reasonValue)
	case "revoke-group":
		return revokeGroup(ctx, client, rest[1:], actorValue, reasonValue)
	case "add-member":
		return addMember(ctx, client, rest[1:], actorValue, reasonValue)
	case "revoke-member":
		return revokeMember(ctx, client, rest[1:], actorValue, reasonValue)
	case "list-subjects":
		return listSubjects(ctx, client, rest[1:])
	case "get-space":
		return getSpace(ctx, client, rest[1:])
	default:
		return fmt.Errorf("unknown command %q", rest[0])
	}
}

// withAssertion attaches a signed assertion for one command. The scope set is
// fixed per command so an invocation cannot acquire more authority than the
// operation needs, and the subject is always stated so the document service can
// make its resource decision.
func withAssertion(ctx context.Context, codec *serviceauth.Codec, caller serviceauth.Caller, subjectKey, actor, command string) (context.Context, error) {
	scopes := []serviceauth.Scope{serviceauth.ScopeDocumentRead}
	switch command {
	case "create-team", "bind-group", "revoke-group", "add-member", "revoke-member":
		scopes = append(scopes, serviceauth.ScopeSpaceAdmin)
	}
	now := time.Now().UTC()
	token, err := codec.SealAssertion(serviceauth.AssertionClaims{
		Caller:       caller,
		Audience:     serviceauth.AudienceDocumentService,
		Scopes:       scopeNames(scopes),
		SubjectKey:   subjectKey,
		Conversation: &serviceauth.Conversation{Kind: serviceauth.ConversationPrivate},
		Actor:        actor,
		IssuedAt:     now.Unix(),
		ExpiresAt:    now.Add(2 * time.Minute).Unix(),
	})
	if err != nil {
		return nil, err
	}
	return metadata.AppendToOutgoingContext(ctx, serviceauth.HeaderAuthorization, serviceauth.AuthorizationHeader(token)), nil
}

func scopeNames(scopes []serviceauth.Scope) []string {
	names := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		names = append(names, string(scope))
	}
	return names
}

func createTeam(ctx context.Context, client documentv1.DocumentServiceClient, args []string, subjectKey, actor, reason string) error {
	flags := flag.NewFlagSet("create-team", flag.ContinueOnError)
	owner := flags.String("owner-subject", "", "owner subject key; defaults to -subject")
	name := flags.String("name", "", "team space name")
	requestID := flags.String("request-id", "", "idempotency hint")
	if err := flags.Parse(args); err != nil {
		return err
	}
	// The owner is whichever subject the assertion names. -owner-subject only
	// changes what the operator sees in the confirmation, so a mismatch between
	// the two would be misleading; it is rejected rather than ignored.
	ownerValue := strings.TrimSpace(*owner)
	expected := strings.TrimSpace(subjectKey)
	if ownerValue != "" && ownerValue != expected {
		return fmt.Errorf("-owner-subject %q does not match -subject %q; the assertion decides the owner", ownerValue, expected)
	}
	response, err := client.CreateTeamSpace(ctx, &documentv1.CreateTeamSpaceRequest{Name: *name, RequestId: *requestID})
	if err != nil {
		return err
	}
	fmt.Printf("team space created: %s (owner %s, actor %s, reason %s)\n",
		response.GetSpace().GetSpaceId(), expected, actor, reason)
	return nil
}

func bindGroup(ctx context.Context, client documentv1.DocumentServiceClient, args []string, actor, reason string) error {
	flags := flag.NewFlagSet("bind-group", flag.ContinueOnError)
	botID := flags.String("bot-id", "", "QQ Bot id")
	groupID := flags.String("external-group-id", "", "QQ group id")
	spaceID := flags.String("space-id", "", "team space UUID")
	requestID := flags.String("request-id", "", "idempotency hint")
	if err := flags.Parse(args); err != nil {
		return err
	}
	response, err := client.BindGroupSpace(ctx, &documentv1.BindGroupSpaceRequest{
		BotId: *botID, ExternalGroupId: *groupID, SpaceId: *spaceID,
		Reason: reason, RequestId: *requestID,
	})
	if err != nil {
		return err
	}
	fmt.Printf("group %s bound to space %s (binding %s)\n", *groupID, *spaceID, response.GetBinding().GetBindingId())
	return nil
}

func revokeGroup(ctx context.Context, client documentv1.DocumentServiceClient, args []string, actor, reason string) error {
	flags := flag.NewFlagSet("revoke-group", flag.ContinueOnError)
	botID := flags.String("bot-id", "", "QQ Bot id")
	groupID := flags.String("external-group-id", "", "QQ group id")
	requestID := flags.String("request-id", "", "idempotency hint")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if _, err := client.RevokeGroupSpace(ctx, &documentv1.RevokeGroupSpaceRequest{
		BotId: *botID, ExternalGroupId: *groupID, Reason: reason, RequestId: *requestID,
	}); err != nil {
		return err
	}
	fmt.Printf("group %s binding revoked\n", *groupID)
	return nil
}

func addMember(ctx context.Context, client documentv1.DocumentServiceClient, args []string, actor, reason string) error {
	flags := flag.NewFlagSet("add-member", flag.ContinueOnError)
	spaceID := flags.String("space-id", "", "team space UUID")
	subject := flags.String("subject", "", "member subject key, e.g. qq:user:10001/20002")
	role := flags.String("role", "member", "member role: owner|admin|member")
	requestID := flags.String("request-id", "", "idempotency hint")
	if err := flags.Parse(args); err != nil {
		return err
	}
	memberRole, err := parseRole(*role)
	if err != nil {
		return err
	}
	if _, err := client.GrantSpaceMember(ctx, &documentv1.GrantSpaceMemberRequest{
		SpaceId: *spaceID, SubjectKey: *subject, MemberRole: memberRole, RequestId: *requestID,
	}); err != nil {
		return err
	}
	fmt.Printf("subject %s added to space %s as %s (actor %s, reason %s)\n", *subject, *spaceID, *role, actor, reason)
	return nil
}

func revokeMember(ctx context.Context, client documentv1.DocumentServiceClient, args []string, actor, reason string) error {
	flags := flag.NewFlagSet("revoke-member", flag.ContinueOnError)
	spaceID := flags.String("space-id", "", "team space UUID")
	subject := flags.String("subject", "", "member subject key")
	requestID := flags.String("request-id", "", "idempotency hint")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if _, err := client.RevokeSpaceMember(ctx, &documentv1.RevokeSpaceMemberRequest{
		SpaceId: *spaceID, SubjectKey: *subject, RequestId: *requestID,
	}); err != nil {
		return err
	}
	fmt.Printf("subject %s removed from space %s (actor %s, reason %s)\n", *subject, *spaceID, actor, reason)
	return nil
}

func listSubjects(ctx context.Context, client documentv1.DocumentServiceClient, args []string) error {
	flags := flag.NewFlagSet("list-subjects", flag.ContinueOnError)
	subject := flags.String("subject", "", "exact subject key; empty lists the registry page")
	if err := flags.Parse(args); err != nil {
		return err
	}
	response, err := client.ListSubjects(ctx, &documentv1.ListSubjectsRequest{SubjectKey: *subject})
	if err != nil {
		return err
	}
	for _, entry := range response.GetSubjects() {
		fmt.Printf("%-48s origin=%-6s type=%-6s active=%t name=%q\n",
			entry.GetSubjectKey(), entry.GetOrigin(), entry.GetSubjectType(), entry.GetActive(), entry.GetDisplayName())
	}
	if len(response.GetSubjects()) == 0 {
		fmt.Println("no subjects")
	}
	return nil
}

func getSpace(ctx context.Context, client documentv1.DocumentServiceClient, args []string) error {
	flags := flag.NewFlagSet("get-space", flag.ContinueOnError)
	spaceID := flags.String("space-id", "", "team space UUID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	response, err := client.GetSpace(ctx, &documentv1.GetSpaceRequest{SpaceId: *spaceID})
	if err != nil {
		return err
	}
	space := response.GetSpace()
	fmt.Printf("space %s type=%s owner=%s name=%q\n", space.GetSpaceId(), space.GetSpaceType(), space.GetOwnerSubjectKey(), space.GetName())
	for _, member := range response.GetMembers() {
		fmt.Printf("  member %-48s role=%-8s revoked=%v\n", member.GetSubjectKey(), member.GetMemberRole(), member.GetRevokedAt() != "")
	}
	for _, binding := range response.GetGroupBindings() {
		fmt.Printf("  group  bot=%s group=%s space=%s revoked=%v\n",
			binding.GetBotId(), binding.GetExternalGroupId(), binding.GetSpaceId(), binding.GetRevokedAt() != "")
	}
	return nil
}

func parseRole(raw string) (documentv1.MemberRole, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "owner":
		return documentv1.MemberRole_MEMBER_ROLE_OWNER, nil
	case "admin":
		return documentv1.MemberRole_MEMBER_ROLE_ADMIN, nil
	case "member", "":
		return documentv1.MemberRole_MEMBER_ROLE_MEMBER, nil
	default:
		return documentv1.MemberRole_MEMBER_ROLE_UNSPECIFIED, fmt.Errorf("unknown role %q", raw)
	}
}

func readKey(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("a boundary key is required: pass -capability-key-file or set DOCUMENT_SERVICE_CAPABILITY_KEY_FILE")
	}
	raw, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return nil, fmt.Errorf("read boundary key %s: %w", path, err)
	}
	key := []byte(strings.TrimSpace(string(raw)))
	if len(key) < 32 {
		return nil, fmt.Errorf("boundary key %s must be at least 32 bytes", path)
	}
	return key, nil
}
