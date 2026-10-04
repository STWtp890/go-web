package application

import (
	"context"
	"fmt"
	"strings"

	"document-service/internal/domain"

	documentv1 "packages/gen/document/v1"
	"packages/serviceauth"

	"github.com/jackc/pgx/v5"
)

// Audit targets are rendered as "<kind>:<id>" so the previous_target / new_target
// columns stay comparable across entity kinds without a join.
const (
	targetSpace   = "space:"
	targetSubject = "subject:"
)

// createTeamSpace implements the CreateTeamSpace use case: the space and its owner
// membership are created in one transaction, because a deferred constraint trigger
// refuses a space without an active owner.
func (service *Service) createTeamSpace(ctx context.Context, principal *serviceauth.Principal, request *documentv1.CreateTeamSpaceRequest) (*documentv1.CreateTeamSpaceResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: a create space request is required", domain.ErrInvalidInput)
	}
	caller, err := service.verifyCaller(principal, request.GetRequestId())
	if err != nil {
		return nil, err
	}
	name, err := domain.ValidateSpaceName(request.GetName())
	if err != nil {
		return nil, err
	}

	now := service.now().UTC()
	spaceID := service.newID()
	var created domain.Space

	err = service.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := service.ensureSubject(ctx, tx, caller.Subject, caller); err != nil {
			return err
		}
		created = domain.Space{
			SpaceID:         spaceID,
			OwnerSubjectKey: caller.Subject.Key,
			SpaceType:       domain.SpaceTypeTeam,
			Name:            name,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if err := service.store.InsertSpace(ctx, tx, created); err != nil {
			return err
		}
		if err := service.store.InsertMember(ctx, tx, domain.Member{
			SpaceID:    spaceID,
			SubjectKey: caller.Subject.Key,
			MemberRole: domain.RoleOwner,
			CreatedAt:  now,
			UpdatedAt:  now,
		}); err != nil {
			return err
		}
		return service.appendAudit(ctx, tx, domain.AuditEvent{
			SubjectType: domain.AuditSubjectTypeSpace,
			Action:      domain.AuditActionCreate,
			SubjectKey:  caller.Subject.Key,
			SpaceID:     spaceID,
			NewTarget:   targetSpace + spaceID,
			Actor:       caller.Actor,
			Source:      caller.Source,
			Reason:      "team space created",
			RequestID:   caller.RequestID,
		})
	})
	if err != nil {
		return nil, err
	}
	return &documentv1.CreateTeamSpaceResponse{Space: spaceProto(created)}, nil
}

// getSpace implements the GetSpace use case: the space, its members and its active
// group bindings. It is an administrative read covered by the document.read scope,
// not a resource decision, which is why it does not run the document read rule.
func (service *Service) getSpace(ctx context.Context, principal *serviceauth.Principal, request *documentv1.GetSpaceRequest) (*documentv1.GetSpaceResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: a get space request is required", domain.ErrInvalidInput)
	}
	if _, err := service.verifyCaller(principal, ""); err != nil {
		return nil, err
	}
	spaceID, err := domain.ValidateUUID(request.GetSpaceId(), "space id")
	if err != nil {
		return nil, err
	}

	var (
		space    domain.Space
		members  []domain.Member
		bindings []domain.Binding
	)
	err = service.store.InReadOnly(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if space, err = service.store.GetSpace(ctx, tx, spaceID); err != nil {
			return err
		}
		if members, err = service.store.ListMembers(ctx, tx, spaceID); err != nil {
			return err
		}
		bindings, err = service.store.ListActiveBindings(ctx, tx, spaceID)
		return err
	})
	if err != nil {
		return nil, err
	}

	response := &documentv1.GetSpaceResponse{
		Space:         spaceProto(space),
		Members:       make([]*documentv1.SpaceMember, 0, len(members)),
		GroupBindings: make([]*documentv1.GroupSpaceBinding, 0, len(bindings)),
	}
	for _, member := range members {
		response.Members = append(response.Members, memberProto(member))
	}
	for _, binding := range bindings {
		response.GroupBindings = append(response.GroupBindings, bindingProto(binding))
	}
	return response, nil
}

// listSubjects implements the ListSubjects use case: the document service's own
// access subject registry, by exact key or as a bounded page.
func (service *Service) listSubjects(ctx context.Context, principal *serviceauth.Principal, request *documentv1.ListSubjectsRequest) (*documentv1.ListSubjectsResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: a list subjects request is required", domain.ErrInvalidInput)
	}
	if _, err := service.verifyCaller(principal, ""); err != nil {
		return nil, err
	}
	keyFilter := strings.TrimSpace(request.GetSubjectKey())
	if keyFilter != "" {
		identity, err := domain.ParseSubjectKey(keyFilter)
		if err != nil {
			return nil, err
		}
		keyFilter = identity.Key
	}

	var subjects []domain.Subject
	err := service.store.InReadOnly(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		subjects, err = service.store.ListSubjects(ctx, tx, keyFilter, 0)
		return err
	})
	if err != nil {
		return nil, err
	}

	response := &documentv1.ListSubjectsResponse{Subjects: make([]*documentv1.AccessSubject, 0, len(subjects))}
	for _, subject := range subjects {
		response.Subjects = append(response.Subjects, subjectProto(subject))
	}
	return response, nil
}

// grantSpaceMember implements the GrantSpaceMember use case.
//
// This is the only path that creates membership. It is never called implicitly:
// joining a QQ group, receiving a group event or binding a group to a space all
// leave document_service.space_members untouched, which is the resource-side
// invariant of ADR-016 v5: nothing that happens on the channel side may widen
// document access.
func (service *Service) grantSpaceMember(ctx context.Context, principal *serviceauth.Principal, request *documentv1.GrantSpaceMemberRequest) (*documentv1.GrantSpaceMemberResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: a grant request is required", domain.ErrInvalidInput)
	}
	caller, err := service.verifyCaller(principal, request.GetRequestId())
	if err != nil {
		return nil, err
	}
	spaceID, err := domain.ValidateUUID(request.GetSpaceId(), "space id")
	if err != nil {
		return nil, err
	}
	memberIdentity, err := domain.ParseSubjectKey(request.GetSubjectKey())
	if err != nil {
		return nil, err
	}
	role, err := memberRoleFromProto(request.GetMemberRole())
	if err != nil {
		return nil, err
	}

	now := service.now().UTC()
	var member domain.Member
	err = service.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := service.ensureSubject(ctx, tx, caller.Subject, caller); err != nil {
			return err
		}
		space, err := service.store.LockSpace(ctx, tx, spaceID)
		if err != nil {
			return err
		}
		if space.SpaceType != domain.SpaceTypeTeam {
			return fmt.Errorf("%w: a private space may only contain its owner", domain.ErrPrecondition)
		}
		// The member has to exist in the registry before it can be referenced; the
		// foreign key would refuse the row otherwise.
		if memberIdentity.Key != caller.Subject.Key {
			if err := service.ensureSubject(ctx, tx, memberIdentity, caller); err != nil {
				return err
			}
		}

		existing, found, err := service.store.GetMember(ctx, tx, spaceID, memberIdentity.Key)
		if err != nil {
			return err
		}
		switch {
		case found && existing.RevokedAt == nil:
			return fmt.Errorf("%w: subject %s is already an active member of space %s",
				domain.ErrAlreadyExists, memberIdentity.Key, spaceID)
		case found:
			restored, err := service.store.RestoreMember(ctx, tx, spaceID, memberIdentity.Key, role, now)
			if err != nil {
				return err
			}
			if !restored {
				return fmt.Errorf("%w: membership of %s in space %s could not be restored",
					domain.ErrPrecondition, memberIdentity.Key, spaceID)
			}
			member = domain.Member{
				SpaceID:    spaceID,
				SubjectKey: memberIdentity.Key,
				MemberRole: role,
				CreatedAt:  existing.CreatedAt,
				UpdatedAt:  now,
			}
		default:
			member = domain.Member{
				SpaceID:    spaceID,
				SubjectKey: memberIdentity.Key,
				MemberRole: role,
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			if err := service.store.InsertMember(ctx, tx, member); err != nil {
				return err
			}
		}

		return service.appendAudit(ctx, tx, domain.AuditEvent{
			SubjectType: domain.AuditSubjectTypeSpaceMember,
			Action:      domain.AuditActionGrant,
			SubjectKey:  memberIdentity.Key,
			SpaceID:     spaceID,
			NewTarget:   targetSubject + memberIdentity.Key,
			Actor:       caller.Actor,
			Source:      caller.Source,
			Reason:      "space membership granted",
			RequestID:   caller.RequestID,
		})
	})
	if err != nil {
		return nil, err
	}
	return &documentv1.GrantSpaceMemberResponse{Member: memberProto(member)}, nil
}

// revokeSpaceMember implements the RevokeSpaceMember use case. An owner membership
// is not revocable: a space without an active owner is refused by the deferred
// trigger, and ownership moves by creating a space, not by leaving one.
func (service *Service) revokeSpaceMember(ctx context.Context, principal *serviceauth.Principal, request *documentv1.RevokeSpaceMemberRequest) (*documentv1.RevokeSpaceMemberResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: a revoke request is required", domain.ErrInvalidInput)
	}
	caller, err := service.verifyCaller(principal, request.GetRequestId())
	if err != nil {
		return nil, err
	}
	spaceID, err := domain.ValidateUUID(request.GetSpaceId(), "space id")
	if err != nil {
		return nil, err
	}
	memberIdentity, err := domain.ParseSubjectKey(request.GetSubjectKey())
	if err != nil {
		return nil, err
	}

	now := service.now().UTC()
	var member domain.Member
	err = service.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := service.ensureSubject(ctx, tx, caller.Subject, caller); err != nil {
			return err
		}
		if _, err := service.store.LockSpace(ctx, tx, spaceID); err != nil {
			return err
		}
		existing, found, err := service.store.GetMember(ctx, tx, spaceID, memberIdentity.Key)
		if err != nil {
			return err
		}
		if !found || existing.RevokedAt != nil {
			return fmt.Errorf("%w: subject %s is not an active member of space %s",
				domain.ErrNotFound, memberIdentity.Key, spaceID)
		}
		if existing.MemberRole == domain.RoleOwner {
			return fmt.Errorf("%w: owner membership of space %s cannot be revoked", domain.ErrPrecondition, spaceID)
		}
		closed, err := service.store.RevokeMember(ctx, tx, spaceID, memberIdentity.Key, now)
		if err != nil {
			return err
		}
		if !closed {
			return fmt.Errorf("%w: membership of %s in space %s was not active",
				domain.ErrNotFound, memberIdentity.Key, spaceID)
		}
		member = existing
		member.UpdatedAt = now
		member.RevokedAt = &now

		return service.appendAudit(ctx, tx, domain.AuditEvent{
			SubjectType:    domain.AuditSubjectTypeSpaceMember,
			Action:         domain.AuditActionRevoke,
			SubjectKey:     memberIdentity.Key,
			SpaceID:        spaceID,
			PreviousTarget: targetSubject + memberIdentity.Key,
			Actor:          caller.Actor,
			Source:         caller.Source,
			Reason:         "space membership revoked",
			RequestID:      caller.RequestID,
		})
	})
	if err != nil {
		return nil, err
	}
	return &documentv1.RevokeSpaceMemberResponse{Member: memberProto(member)}, nil
}

// bindGroupSpace implements the BindGroupSpace use case.
//
// One active binding per (channel, bot_id, group) and per (channel, bot_id, space).
// Rebinding closes the old row and appends a new one, so a space migration stays
// auditable. The target must be a team space; a private space can therefore never
// become "the current team".
func (service *Service) bindGroupSpace(ctx context.Context, principal *serviceauth.Principal, request *documentv1.BindGroupSpaceRequest) (*documentv1.BindGroupSpaceResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: a bind request is required", domain.ErrInvalidInput)
	}
	caller, err := service.verifyCaller(principal, request.GetRequestId())
	if err != nil {
		return nil, err
	}
	botID := strings.TrimSpace(request.GetBotId())
	if !domain.ValidExternalID(botID) {
		return nil, fmt.Errorf("%w: bot id is required and must be at most %d characters",
			domain.ErrInvalidInput, domain.MaxExternalIDLength)
	}
	externalGroupID := strings.TrimSpace(request.GetExternalGroupId())
	if !domain.ValidExternalID(externalGroupID) {
		return nil, fmt.Errorf("%w: external group id is required and must be at most %d characters",
			domain.ErrInvalidInput, domain.MaxExternalIDLength)
	}
	spaceID, err := domain.ValidateUUID(request.GetSpaceId(), "space id")
	if err != nil {
		return nil, err
	}
	reason, err := domain.ValidateReason(request.GetReason())
	if err != nil {
		return nil, err
	}

	now := service.now().UTC()
	var binding domain.Binding
	err = service.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := service.ensureSubject(ctx, tx, caller.Subject, caller); err != nil {
			return err
		}
		// Serialize the read-decide-write sequence for this group so two concurrent
		// binds cannot both decide they are the first one.
		if err := service.store.LockGroupBinding(ctx, tx, domain.ChannelQQ, botID, externalGroupID); err != nil {
			return err
		}
		space, err := service.store.LockSpace(ctx, tx, spaceID)
		if err != nil {
			return err
		}
		if space.SpaceType != domain.SpaceTypeTeam {
			return fmt.Errorf("%w: QQ groups may only bind to team spaces", domain.ErrPrecondition)
		}

		active, found, err := service.store.GetActiveGroupBinding(ctx, tx, domain.ChannelQQ, botID, externalGroupID)
		if err != nil {
			return err
		}
		action := domain.AuditActionBind
		previousTarget := ""
		if found {
			if active.SpaceID == spaceID {
				return fmt.Errorf("%w: group %s is already bound to space %s in bot %s",
					domain.ErrAlreadyExists, externalGroupID, spaceID, botID)
			}
			action = domain.AuditActionRebind
			previousTarget = targetSpace + active.SpaceID
		}
		// A space can only be the target of one active binding per Bot.
		occupied, found, err := service.store.GetActiveSpaceBinding(ctx, tx, domain.ChannelQQ, botID, spaceID)
		if err != nil {
			return err
		}
		if found && occupied.ExternalGroupID != externalGroupID {
			return fmt.Errorf("%w: space %s is already bound to group %s in bot %s",
				domain.ErrAlreadyExists, spaceID, occupied.ExternalGroupID, botID)
		}
		if action == domain.AuditActionRebind {
			closed, err := service.store.RevokeBinding(ctx, tx, active.BindingID, now)
			if err != nil {
				return err
			}
			if !closed {
				return fmt.Errorf("%w: binding %s was not active", domain.ErrPrecondition, active.BindingID)
			}
		}

		binding = domain.Binding{
			BindingID:       service.newID(),
			Channel:         domain.ChannelQQ,
			BotID:           botID,
			ExternalGroupID: externalGroupID,
			SpaceID:         spaceID,
			Actor:           caller.Actor,
			Source:          caller.Source,
			Reason:          reason,
			CreatedAt:       now,
		}
		if err := service.store.InsertBinding(ctx, tx, binding); err != nil {
			return err
		}
		return service.appendAudit(ctx, tx, domain.AuditEvent{
			SubjectType:    domain.AuditSubjectTypeGroupSpaceBinding,
			Action:         action,
			SubjectKey:     caller.Subject.Key,
			Channel:        domain.ChannelQQ,
			BotID:          botID,
			ExternalID:     externalGroupID,
			SpaceID:        spaceID,
			PreviousTarget: previousTarget,
			NewTarget:      targetSpace + spaceID,
			Actor:          caller.Actor,
			Source:         caller.Source,
			Reason:         reason,
			RequestID:      caller.RequestID,
		})
	})
	if err != nil {
		return nil, err
	}
	return &documentv1.BindGroupSpaceResponse{Binding: bindingProto(binding)}, nil
}

// revokeGroupSpace implements the RevokeGroupSpace use case. Revocation takes
// effect on the next resolution: it closes the binding row, it does not touch the
// membership table and it does not reindex anything.
func (service *Service) revokeGroupSpace(ctx context.Context, principal *serviceauth.Principal, request *documentv1.RevokeGroupSpaceRequest) (*documentv1.RevokeGroupSpaceResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: a revoke group request is required", domain.ErrInvalidInput)
	}
	caller, err := service.verifyCaller(principal, request.GetRequestId())
	if err != nil {
		return nil, err
	}
	botID := strings.TrimSpace(request.GetBotId())
	if !domain.ValidExternalID(botID) {
		return nil, fmt.Errorf("%w: bot id is required", domain.ErrInvalidInput)
	}
	externalGroupID := strings.TrimSpace(request.GetExternalGroupId())
	if !domain.ValidExternalID(externalGroupID) {
		return nil, fmt.Errorf("%w: external group id is required", domain.ErrInvalidInput)
	}
	reason, err := domain.ValidateReason(request.GetReason())
	if err != nil {
		return nil, err
	}

	now := service.now().UTC()
	var binding domain.Binding
	err = service.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := service.ensureSubject(ctx, tx, caller.Subject, caller); err != nil {
			return err
		}
		if err := service.store.LockGroupBinding(ctx, tx, domain.ChannelQQ, botID, externalGroupID); err != nil {
			return err
		}
		active, found, err := service.store.GetActiveGroupBinding(ctx, tx, domain.ChannelQQ, botID, externalGroupID)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: group %s has no active binding in bot %s", domain.ErrNotFound, externalGroupID, botID)
		}
		closed, err := service.store.RevokeBinding(ctx, tx, active.BindingID, now)
		if err != nil {
			return err
		}
		if !closed {
			return fmt.Errorf("%w: binding %s was not active", domain.ErrPrecondition, active.BindingID)
		}
		binding = active
		binding.RevokedAt = &now

		return service.appendAudit(ctx, tx, domain.AuditEvent{
			SubjectType:    domain.AuditSubjectTypeGroupSpaceBinding,
			Action:         domain.AuditActionRevoke,
			SubjectKey:     caller.Subject.Key,
			Channel:        domain.ChannelQQ,
			BotID:          botID,
			ExternalID:     externalGroupID,
			SpaceID:        active.SpaceID,
			PreviousTarget: targetSpace + active.SpaceID,
			Actor:          caller.Actor,
			Source:         caller.Source,
			Reason:         reason,
			RequestID:      caller.RequestID,
		})
	})
	if err != nil {
		return nil, err
	}
	return &documentv1.RevokeGroupSpaceResponse{Binding: bindingProto(binding)}, nil
}
