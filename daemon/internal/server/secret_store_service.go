package server

import (
	"context"
	"errors"
	"net/http"

	connect "connectrpc.com/connect"
	daemonv1 "github.com/solidarity-ai/toolbox/daemon/apiv1"
	"github.com/solidarity-ai/toolbox/daemon/apiv1/daemonv1connect"
	"github.com/solidarity-ai/toolbox/secrets"
)

type SecretStoreService struct {
	daemonv1connect.UnimplementedSecretStoreServiceHandler

	store       secrets.ManagedSecretStore
	secretEpoch *SecretEpoch
	notifier    *stateNotifier
}

func NewSecretStoreService(store secrets.ManagedSecretStore) *SecretStoreService {
	return newSecretStoreService(store, NewSecretEpoch(), newStateNotifier())
}

func newSecretStoreService(store secrets.ManagedSecretStore, secretEpoch *SecretEpoch, notifier *stateNotifier) *SecretStoreService {
	if store == nil {
		store = secrets.NewLocalSecretStore("", "")
	}
	if secretEpoch == nil {
		secretEpoch = NewSecretEpoch()
	}
	if notifier == nil {
		notifier = newStateNotifier()
	}
	return &SecretStoreService{
		store:       store,
		secretEpoch: secretEpoch,
		notifier:    notifier,
	}
}

func (s *SecretStoreService) Handler(opts ...connect.HandlerOption) (string, http.Handler) {
	return daemonv1connect.NewSecretStoreServiceHandler(s, opts...)
}

func (s *SecretStoreService) Unlock(ctx context.Context, req *connect.Request[daemonv1.SecretUnlockRequest]) (*connect.Response[daemonv1.SecretUnlockResponse], error) {
	if err := s.UnlockStore(ctx, req.Msg.GetUnlockKey()); err != nil {
		return nil, secretStoreConnectError(err)
	}
	return connect.NewResponse(&daemonv1.SecretUnlockResponse{}), nil
}

func (s *SecretStoreService) Lock(ctx context.Context, req *connect.Request[daemonv1.SecretLockRequest]) (*connect.Response[daemonv1.SecretLockResponse], error) {
	if err := s.LockStore(ctx); err != nil {
		return nil, secretStoreConnectError(err)
	}
	return connect.NewResponse(&daemonv1.SecretLockResponse{}), nil
}

func (s *SecretStoreService) Get(ctx context.Context, req *connect.Request[daemonv1.SecretGetRequest]) (*connect.Response[daemonv1.SecretGetResponse], error) {
	value, err := s.store.Get(ctx, req.Msg.GetKey())
	if err != nil {
		return nil, secretStoreConnectError(err)
	}
	return connect.NewResponse(&daemonv1.SecretGetResponse{Value: value}), nil
}

func (s *SecretStoreService) Set(ctx context.Context, req *connect.Request[daemonv1.SecretSetRequest]) (*connect.Response[daemonv1.SecretSetResponse], error) {
	if err := s.store.Set(ctx, req.Msg.GetKey(), req.Msg.GetValue()); err != nil {
		return nil, secretStoreConnectError(err)
	}
	s.markSecretChanged()
	return connect.NewResponse(&daemonv1.SecretSetResponse{}), nil
}

func (s *SecretStoreService) Delete(ctx context.Context, req *connect.Request[daemonv1.SecretDeleteRequest]) (*connect.Response[daemonv1.SecretDeleteResponse], error) {
	if err := s.store.Delete(ctx, req.Msg.GetKey()); err != nil {
		return nil, secretStoreConnectError(err)
	}
	s.markSecretChanged()
	return connect.NewResponse(&daemonv1.SecretDeleteResponse{}), nil
}

func (s *SecretStoreService) List(ctx context.Context, req *connect.Request[daemonv1.SecretListRequest]) (*connect.Response[daemonv1.SecretListResponse], error) {
	keys, err := s.store.List(ctx, req.Msg.GetPrefix())
	if err != nil {
		return nil, secretStoreConnectError(err)
	}
	return connect.NewResponse(&daemonv1.SecretListResponse{Keys: keys}), nil
}

func (s *SecretStoreService) UnlockStore(ctx context.Context, unlockKey string) error {
	if s == nil {
		return nil
	}
	if err := s.store.Unlock(ctx, unlockKey); err != nil {
		return err
	}
	s.markSecretChanged()
	return nil
}

func (s *SecretStoreService) LockStore(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if err := s.store.Lock(ctx); err != nil {
		return err
	}
	s.markSecretChanged()
	return nil
}

func (s *SecretStoreService) Locked(ctx context.Context) (bool, error) {
	if s == nil {
		return true, nil
	}
	_, err := s.store.List(ctx, "")
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, secrets.ErrLocked):
		return true, nil
	default:
		return false, err
	}
}

func (s *SecretStoreService) markSecretChanged() {
	if s == nil {
		return
	}
	if s.secretEpoch != nil {
		s.secretEpoch.Increment()
	}
	if s.notifier != nil {
		s.notifier.Notify()
	}
}

func secretStoreConnectError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, err)
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, err)
	case errors.Is(err, secrets.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, secrets.ErrInvalidKey):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, secrets.ErrLocked):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
