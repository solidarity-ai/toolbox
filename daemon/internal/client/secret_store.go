package client

import (
	"context"
	"errors"
	"strings"

	connect "connectrpc.com/connect"
	daemonv1 "github.com/solidarity-ai/toolbox/daemon/apiv1"
	"github.com/solidarity-ai/toolbox/daemon/apiv1/daemonv1connect"
	"github.com/solidarity-ai/toolbox/secrets"
)

var _ secrets.ManagedSecretStore = (*SecretStore)(nil)

func (s *SecretStore) Unlock(ctx context.Context, unlockKey string) error {
	return s.withSecretStore(ctx, func(service daemonv1connect.SecretStoreServiceClient) error {
		err := s.unlockWithService(ctx, service, unlockKey)
		if errors.Is(err, secrets.ErrNotInitialized) {
			if s.backupCodeWriter == nil {
				return err
			}
			codes, setupErr := s.setupWithService(ctx, service, unlockKey)
			if setupErr == nil {
				secrets.WriteBackupCodes(s.backupCodeWriter, codes)
			}
			return setupErr
		}
		return err
	})
}

func (s *SecretStore) Lock(ctx context.Context) error {
	return s.withSecretStore(ctx, func(service daemonv1connect.SecretStoreServiceClient) error {
		_, err := service.Lock(ctx, connect.NewRequest(&daemonv1.SecretLockRequest{}))
		return mapSecretStoreError(err)
	})
}

func (s *SecretStore) Get(ctx context.Context, key string) ([]byte, error) {
	var value []byte
	err := s.withAutoUnlock(ctx, func(service daemonv1connect.SecretStoreServiceClient) error {
		resp, err := service.Get(ctx, connect.NewRequest(&daemonv1.SecretGetRequest{Key: key}))
		if err != nil {
			return mapSecretStoreError(err)
		}
		value = append([]byte(nil), resp.Msg.GetValue()...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return value, nil
}

func (s *SecretStore) Set(ctx context.Context, key string, value []byte) error {
	return s.withAutoUnlock(ctx, func(service daemonv1connect.SecretStoreServiceClient) error {
		_, err := service.Set(ctx, connect.NewRequest(&daemonv1.SecretSetRequest{
			Key:   key,
			Value: value,
		}))
		return mapSecretStoreError(err)
	})
}

func (s *SecretStore) Delete(ctx context.Context, key string) error {
	return s.withAutoUnlock(ctx, func(service daemonv1connect.SecretStoreServiceClient) error {
		_, err := service.Delete(ctx, connect.NewRequest(&daemonv1.SecretDeleteRequest{Key: key}))
		return mapSecretStoreError(err)
	})
}

func (s *SecretStore) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	err := s.withAutoUnlock(ctx, func(service daemonv1connect.SecretStoreServiceClient) error {
		resp, err := service.List(ctx, connect.NewRequest(&daemonv1.SecretListRequest{Prefix: prefix}))
		if err != nil {
			return mapSecretStoreError(err)
		}
		keys = append([]string(nil), resp.Msg.GetKeys()...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}

func (s *SecretStore) withAutoUnlock(ctx context.Context, fn func(service daemonv1connect.SecretStoreServiceClient) error) error {
	return s.withSecretStore(ctx, func(service daemonv1connect.SecretStoreServiceClient) error {
		err := fn(service)
		if strings.TrimSpace(s.unlockKey) == "" || (!errors.Is(err, secrets.ErrLocked) && !errors.Is(err, secrets.ErrNotInitialized)) {
			return err
		}
		if unlockErr := s.unlockWithService(ctx, service, s.unlockKey); unlockErr != nil {
			if errors.Is(unlockErr, secrets.ErrNotInitialized) {
				if s.backupCodeWriter == nil {
					return unlockErr
				}
				codes, setupErr := s.setupWithService(ctx, service, s.unlockKey)
				if setupErr != nil {
					return setupErr
				}
				secrets.WriteBackupCodes(s.backupCodeWriter, codes)
			} else {
				return unlockErr
			}
		}
		return fn(service)
	})
}

func (s *SecretStore) withSecretStore(ctx context.Context, fn func(service daemonv1connect.SecretStoreServiceClient) error) error {
	client, err := EnsureConnection()
	if err != nil {
		return err
	}
	defer client.Close()
	return fn(client.secretStoreService)
}

func (s *SecretStore) unlockWithService(ctx context.Context, service daemonv1connect.SecretStoreServiceClient, unlockKey string) error {
	_, err := service.Unlock(ctx, connect.NewRequest(&daemonv1.SecretUnlockRequest{
		UnlockKey: unlockKey,
	}))
	return mapSecretStoreError(err)
}

func (s *SecretStore) setupWithService(ctx context.Context, service daemonv1connect.SecretStoreServiceClient, unlockKey string) ([]string, error) {
	resp, err := service.Setup(ctx, connect.NewRequest(&daemonv1.SecretSetupRequest{
		UnlockKey: unlockKey,
	}))
	if err != nil {
		return nil, mapSecretStoreError(err)
	}
	return append([]string(nil), resp.Msg.GetBackupCodes()...), nil
}

func mapSecretStoreError(err error) error {
	if err == nil {
		return nil
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return err
	}
	switch connectErr.Code() {
	case connect.CodeNotFound:
		return secrets.ErrNotFound
	case connect.CodeInvalidArgument:
		return secrets.ErrInvalidKey
	case connect.CodeFailedPrecondition:
		msg := connectErr.Message()
		if strings.Contains(msg, secrets.ErrNotInitialized.Error()) {
			return secrets.ErrNotInitialized
		}
		return secrets.ErrLocked
	case connect.CodeCanceled:
		return context.Canceled
	case connect.CodeDeadlineExceeded:
		return context.DeadlineExceeded
	default:
		return err
	}
}
