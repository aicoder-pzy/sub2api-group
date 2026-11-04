package service

import "context"

func (s *adminServiceImpl) AdminUpdateAPIKeyPromptPolicy(ctx context.Context, keyID int64, policy map[string]string) (*APIKey, error) {
	if err := ValidateAPIKeyPromptPolicy(policy); err != nil {
		return nil, err
	}
	key, err := s.apiKeyRepo.GetByID(ctx, keyID)
	if err != nil {
		return nil, err
	}
	key.PromptPolicy = policy
	if err = s.apiKeyRepo.Update(ctx, key, APIKeyUpdateFields{PromptPolicy: true}); err != nil {
		return nil, err
	}
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByKey(ctx, key.Key)
	}
	return key, nil
}
