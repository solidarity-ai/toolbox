package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

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

func (s *SecretStoreService) Setup(ctx context.Context, req *connect.Request[daemonv1.SecretSetupRequest]) (*connect.Response[daemonv1.SecretSetupResponse], error) {
	codes, err := s.SetupStore(ctx, req.Msg.GetUnlockKey())
	if err != nil {
		return nil, secretStoreConnectError(err)
	}
	return connect.NewResponse(&daemonv1.SecretSetupResponse{BackupCodes: codes}), nil
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

func (s *SecretStoreService) SetupStore(ctx context.Context, unlockKey string) ([]string, error) {
	if s == nil {
		return nil, nil
	}
	setupper, ok := s.store.(interface {
		Setup(context.Context, string) ([]string, error)
	})
	if !ok {
		return nil, secrets.ErrNotInitialized
	}
	codes, err := setupper.Setup(ctx, unlockKey)
	if err != nil {
		return nil, err
	}
	s.markSecretChanged()
	return codes, nil
}

func (s *SecretStoreService) RecoveryCodes(ctx context.Context, unlockKey string) ([]string, error) {
	if s == nil {
		return nil, nil
	}
	if recoveryState, ok := s.store.(interface {
		RecoveryUnlocked(context.Context) (bool, error)
	}); ok {
		recoveryUnlocked, err := recoveryState.RecoveryUnlocked(ctx)
		if err != nil {
			return nil, err
		}
		if recoveryUnlocked {
			generator, ok := s.store.(interface {
				RecoveryCodes(context.Context) ([]string, error)
			})
			if !ok {
				return nil, secrets.ErrLocked
			}
			codes, err := generator.RecoveryCodes(ctx)
			if err != nil {
				return nil, err
			}
			s.markSecretChanged()
			return codes, nil
		}
	}
	if strings.TrimSpace(unlockKey) == "" {
		return nil, secrets.ErrLocked
	}
	generator, ok := s.store.(interface {
		BackupCodes(context.Context, string) ([]string, error)
	})
	if !ok {
		return nil, secrets.ErrLocked
	}
	codes, err := generator.BackupCodes(ctx, unlockKey)
	if err != nil {
		return nil, err
	}
	s.markSecretChanged()
	return codes, nil
}

func (s *SecretStoreService) RecoveryUnlocked(ctx context.Context) (bool, error) {
	if s == nil {
		return false, nil
	}
	recoveryState, ok := s.store.(interface {
		RecoveryUnlocked(context.Context) (bool, error)
	})
	if !ok {
		return false, nil
	}
	return recoveryState.RecoveryUnlocked(ctx)
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
	case errors.Is(err, secrets.ErrLocked), errors.Is(err, secrets.ErrNotInitialized):
		return true, nil
	default:
		return false, err
	}
}

func (s *SecretStoreService) SetupRequired(ctx context.Context) (bool, error) {
	if s == nil {
		return false, nil
	}
	if initializedStore, ok := s.store.(interface{ Initialized() (bool, error) }); ok {
		initialized, err := initializedStore.Initialized()
		return !initialized, err
	}
	_, err := s.store.List(ctx, "")
	switch {
	case errors.Is(err, secrets.ErrNotInitialized):
		return true, nil
	case err == nil, errors.Is(err, secrets.ErrLocked):
		return false, nil
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
	case errors.Is(err, secrets.ErrNotInitialized):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, secrets.ErrLocked):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
