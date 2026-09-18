package grpcadapter

import (
	"context"
	"errors"

	"mixin-search/internal/chat"
	mixinsearchchatv1 "packages/gen/mixin-search/chat/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ChatKnowledgeService is the chat corpus's transport-neutral surface. Keeping
// it an interface means the adapter maps protocol types to the chat control
// plane and nothing else: no state, no lifecycle rules, no storage decisions.
type ChatKnowledgeService interface {
	IndexMessages(context.Context, chat.IndexMessagesRequest) ([]chat.MessageState, error)
	ArchiveConversation(context.Context, chat.ArchiveConversationRequest) (chat.ArchiveResult, error)
	UpdateConversationAccess(context.Context, chat.UpdateConversationAccessRequest) (chat.AccessState, error)
	RetractMessage(context.Context, chat.RetractMessageRequest) (chat.RetractResult, error)
	DeleteConversation(context.Context, chat.DeleteConversationRequest) (chat.DeleteResult, error)
	GetConversationIndexState(context.Context, chat.GetConversationIndexStateRequest) (chat.ConversationIndexState, error)
	SearchMessages(context.Context, chat.SearchMessagesRequest) (chat.SearchMessagesResult, error)
}

// ChatServer adapts the generated chat service to the chat control plane. It is
// a separate server type from the document one so neither corpus's protocol
// surface can reach the other's service.
type ChatServer struct {
	mixinsearchchatv1.UnimplementedChatIndexServiceServer
	service ChatKnowledgeService
}

// NewChatServer builds the chat transport adapter.
func NewChatServer(service ChatKnowledgeService) (*ChatServer, error) {
	if service == nil {
		return nil, errors.New("chat knowledge service is required")
	}
	return &ChatServer{service: service}, nil
}

func (s *ChatServer) IndexConversationMessages(
	ctx context.Context,
	request *mixinsearchchatv1.IndexConversationMessagesRequest,
) (*mixinsearchchatv1.IndexConversationMessagesResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	messages := make([]chat.MessageInput, 0, len(request.GetMessages()))
	for _, message := range request.GetMessages() {
		if message == nil {
			continue
		}
		messages = append(messages, chat.MessageInput{
			MessageID:     message.GetMessageId(),
			SenderID:      message.GetSenderId(),
			SentAtUnixMs:  message.GetSentAtUnixMs(),
			Content:       message.GetContent(),
			ContentSHA256: message.GetContentSha256(),
			Metadata:      message.GetMetadata(),
		})
	}
	states, err := s.service.IndexMessages(ctx, chat.IndexMessagesRequest{
		OperationID:       request.GetOperationId(),
		ConversationID:    request.GetConversationId(),
		OwnerScopeID:      request.GetOwnerScopeId(),
		LifecycleRevision: request.GetLifecycleRevision(),
		Messages:          messages,
	})
	if err != nil {
		return nil, mapChatServiceError(err)
	}
	response := &mixinsearchchatv1.IndexConversationMessagesResponse{
		States: make([]*mixinsearchchatv1.ChatMessageState, 0, len(states)),
	}
	for _, state := range states {
		response.States = append(response.States, chatMessageState(state))
	}
	return response, nil
}

func (s *ChatServer) ArchiveConversation(
	ctx context.Context,
	request *mixinsearchchatv1.ArchiveConversationRequest,
) (*mixinsearchchatv1.ArchiveConversationResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	result, err := s.service.ArchiveConversation(ctx, chat.ArchiveConversationRequest{
		OperationID:       request.GetOperationId(),
		ConversationID:    request.GetConversationId(),
		ArchiveRevision:   request.GetArchiveRevision(),
		LifecycleRevision: request.GetLifecycleRevision(),
	})
	if err != nil {
		return nil, mapChatServiceError(err)
	}
	return &mixinsearchchatv1.ArchiveConversationResponse{
		Status:            conversationStatus(result.Status),
		ArchiveRevision:   result.ArchiveRevision,
		LifecycleRevision: result.LifecycleRevision,
	}, nil
}

func (s *ChatServer) UpdateConversationAccess(
	ctx context.Context,
	request *mixinsearchchatv1.UpdateConversationAccessRequest,
) (*mixinsearchchatv1.UpdateConversationAccessResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	state, err := s.service.UpdateConversationAccess(ctx, chat.UpdateConversationAccessRequest{
		OperationID:       request.GetOperationId(),
		ConversationID:    request.GetConversationId(),
		AccessRevision:    request.GetAccessRevision(),
		LifecycleRevision: request.GetLifecycleRevision(),
		GrantedScopeIDs:   request.GetGrantedScopeIds(),
	})
	if err != nil {
		return nil, mapChatServiceError(err)
	}
	return &mixinsearchchatv1.UpdateConversationAccessResponse{
		State: &mixinsearchchatv1.ConversationAccessState{
			ConversationId:    state.ConversationID,
			AccessRevision:    state.AccessRevision,
			LifecycleRevision: state.LifecycleRevision,
			GrantedScopeIds:   append([]string{}, state.GrantedScopeIDs...),
		},
	}, nil
}

func (s *ChatServer) RetractMessage(
	ctx context.Context,
	request *mixinsearchchatv1.RetractMessageRequest,
) (*mixinsearchchatv1.RetractMessageResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	result, err := s.service.RetractMessage(ctx, chat.RetractMessageRequest{
		OperationID:       request.GetOperationId(),
		ConversationID:    request.GetConversationId(),
		MessageID:         request.GetMessageId(),
		RetractRevision:   request.GetRetractRevision(),
		LifecycleRevision: request.GetLifecycleRevision(),
	})
	if err != nil {
		return nil, mapChatServiceError(err)
	}
	return &mixinsearchchatv1.RetractMessageResponse{
		Retracted:       result.Retracted,
		RetractRevision: result.RetractRevision,
	}, nil
}

func (s *ChatServer) DeleteConversation(
	ctx context.Context,
	request *mixinsearchchatv1.DeleteConversationRequest,
) (*mixinsearchchatv1.DeleteConversationResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	result, err := s.service.DeleteConversation(ctx, chat.DeleteConversationRequest{
		OperationID:       request.GetOperationId(),
		ConversationID:    request.GetConversationId(),
		LifecycleRevision: request.GetLifecycleRevision(),
	})
	if err != nil {
		return nil, mapChatServiceError(err)
	}
	return &mixinsearchchatv1.DeleteConversationResponse{
		Tombstoned:        result.Tombstoned,
		LifecycleRevision: result.LifecycleRevision,
	}, nil
}

func (s *ChatServer) GetConversationIndexState(
	ctx context.Context,
	request *mixinsearchchatv1.GetConversationIndexStateRequest,
) (*mixinsearchchatv1.GetConversationIndexStateResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	state, err := s.service.GetConversationIndexState(ctx, chat.GetConversationIndexStateRequest{
		ConversationID: request.GetConversationId(),
	})
	if err != nil {
		return nil, mapChatServiceError(err)
	}
	return &mixinsearchchatv1.GetConversationIndexStateResponse{
		Exists:                state.Exists,
		Status:                conversationStatus(state.Status),
		OwnerScopeId:          state.OwnerScopeID,
		ArchiveRevision:       state.ArchiveRevision,
		AccessRevision:        state.AccessRevision,
		LifecycleRevision:     state.LifecycleRevision,
		TombstoneRevision:     state.TombstoneRevision,
		Tombstoned:            state.Tombstoned,
		IndexedMessageCount:   int32(state.IndexedMessageCount),
		RetractedMessageCount: int32(state.RetractedMessageCount),
	}, nil
}

func (s *ChatServer) SearchChatMessages(
	ctx context.Context,
	request *mixinsearchchatv1.SearchChatMessagesRequest,
) (*mixinsearchchatv1.SearchChatMessagesResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	result, err := s.service.SearchMessages(ctx, chat.SearchMessagesRequest{
		Query:                  request.GetQuery(),
		AllowedScopeIDs:        request.GetAllowedScopeIds(),
		AllowedConversationIDs: request.GetAllowedConversationIds(),
		SentAfterUnixMs:        request.GetSentAfterUnixMs(),
		SentBeforeUnixMs:       request.GetSentBeforeUnixMs(),
		TopK:                   int(request.GetTopK()),
	})
	if err != nil {
		return nil, mapChatServiceError(err)
	}
	response := &mixinsearchchatv1.SearchChatMessagesResponse{
		Query:     result.Query,
		Truncated: result.Truncated,
		Hits:      make([]*mixinsearchchatv1.ChatSearchHit, 0, len(result.Hits)),
	}
	for _, hit := range result.Hits {
		response.Hits = append(response.Hits, &mixinsearchchatv1.ChatSearchHit{
			Ref: &mixinsearchchatv1.ChatMessageRef{
				ConversationId: hit.ConversationID,
				MessageId:      hit.MessageID,
			},
			OwnerScopeId:  hit.OwnerScopeID,
			SenderId:      hit.SenderID,
			SentAtUnixMs:  hit.SentAtUnixMs,
			Position:      int32(hit.Position),
			Snippet:       hit.Snippet,
			ContentSha256: hit.ContentSHA256,
			RrfScore:      hit.Score,
		})
	}
	return response, nil
}

func chatMessageState(state chat.MessageState) *mixinsearchchatv1.ChatMessageState {
	return &mixinsearchchatv1.ChatMessageState{
		Ref: &mixinsearchchatv1.ChatMessageRef{
			ConversationId: state.ConversationID,
			MessageId:      state.MessageID,
		},
		OwnerScopeId:      state.OwnerScopeID,
		SenderId:          state.SenderID,
		SentAtUnixMs:      state.SentAtUnixMs,
		ArchiveRevision:   state.ArchiveRevision,
		AccessRevision:    state.AccessRevision,
		LifecycleRevision: state.LifecycleRevision,
		RetractRevision:   state.RetractRevision,
		Retracted:         state.Retracted,
		ChunkCount:        int32(state.ChunkCount),
		ContentSha256:     state.ContentSHA256,
	}
}

func conversationStatus(status chat.IndexStatus) mixinsearchchatv1.ConversationIndexStatus {
	switch status {
	case chat.StatusIndexed:
		return mixinsearchchatv1.ConversationIndexStatus_CONVERSATION_INDEX_STATUS_INDEXED
	case chat.StatusArchived:
		return mixinsearchchatv1.ConversationIndexStatus_CONVERSATION_INDEX_STATUS_ARCHIVED
	default:
		return mixinsearchchatv1.ConversationIndexStatus_CONVERSATION_INDEX_STATUS_UNSPECIFIED
	}
}

func mapChatServiceError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	case errors.Is(err, chat.ErrInvalidInput):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, chat.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, chat.ErrControlStoreConflict):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, chat.ErrControlStoreUnavailable), errors.Is(err, chat.ErrProjectionUnavailable):
		return status.Error(codes.Unavailable, err.Error())
	case errors.Is(err, chat.ErrConflict),
		errors.Is(err, chat.ErrStaleLifecycle),
		errors.Is(err, chat.ErrStaleArchive),
		errors.Is(err, chat.ErrStaleAccess),
		errors.Is(err, chat.ErrStaleRetract):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
