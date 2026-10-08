//go:build integration

package repository

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type GatewayCacheSuite struct {
	IntegrationRedisSuite
	cache service.GatewayCache
}

func (s *GatewayCacheSuite) SetupTest() {
	s.IntegrationRedisSuite.SetupTest()
	s.cache = NewGatewayCache(s.rdb)
}

func (s *GatewayCacheSuite) TestGetSessionAccountID_Missing() {
	_, err := s.cache.GetSessionAccountID(s.ctx, 1, "nonexistent")
	require.True(s.T(), errors.Is(err, service.ErrStickySessionNotFound), "expected ErrStickySessionNotFound for missing session")
}

func (s *GatewayCacheSuite) TestSetAndGetSessionAccountID() {
	sessionID := "s1"
	accountID := int64(99)
	groupID := int64(1)
	sessionTTL := 1 * time.Minute

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, sessionTTL), "SetSessionAccountID")

	sid, err := s.cache.GetSessionAccountID(s.ctx, groupID, sessionID)
	require.NoError(s.T(), err, "GetSessionAccountID")
	require.Equal(s.T(), accountID, sid, "session id mismatch")
}

func (s *GatewayCacheSuite) TestGroupModelBindingConcurrentConfirmation() {
	cache := s.cache.(service.GroupModelSchedulingAtomicCache)
	key := service.GroupModelSchedulingKeyPrefix + "bW9kZWwtYQ"
	account, revision, err := cache.GetSessionAccountState(s.ctx, 1, key)
	require.NoError(s.T(), err)
	require.Zero(s.T(), account)
	require.Zero(s.T(), revision)
	var workers sync.WaitGroup
	results := make(chan bool, 20)
	errors := make(chan error, 20)
	for id := int64(1); id <= 20; id++ {
		workers.Add(1)
		go func(id int64) {
			defer workers.Done()
			changed, err := cache.CompareAndSwapSessionAccountID(s.ctx, 1, key, revision, id)
			results <- changed
			errors <- err
		}(id)
	}
	workers.Wait()
	close(results)
	close(errors)
	confirmed := 0
	for changed := range results {
		if changed {
			confirmed++
		}
	}
	for err := range errors {
		require.NoError(s.T(), err)
	}
	require.Equal(s.T(), 1, confirmed)
	account, revision, err = cache.GetSessionAccountState(s.ctx, 1, key)
	require.NoError(s.T(), err)
	require.Positive(s.T(), account)
	require.EqualValues(s.T(), 1, revision)
	ttl, err := s.rdb.TTL(s.ctx, buildSessionKey(1, key)).Result()
	require.NoError(s.T(), err)
	require.Equal(s.T(), -time.Nanosecond, ttl)
}

func (s *GatewayCacheSuite) TestGroupModelManualRefreshAndDeletionRejectStaleConfirmation() {
	cache := s.cache.(service.GroupModelSchedulingAtomicCache)
	key := service.GroupModelSchedulingKeyPrefix + "bW9kZWwtYQ"
	// Existing deployments have a plain binding and no revision key yet.
	require.NoError(s.T(), s.rdb.Set(s.ctx, buildSessionKey(1, key), 7, 0).Err())
	account, revision, err := cache.GetSessionAccountState(s.ctx, 1, key)
	require.NoError(s.T(), err)
	require.EqualValues(s.T(), 7, account)
	require.Zero(s.T(), revision)
	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, 1, key, 7, 0))
	changed, err := cache.CompareAndSwapSessionAccountID(s.ctx, 1, key, revision, 8)
	require.NoError(s.T(), err)
	require.False(s.T(), changed, "even a refresh to the same account invalidates old requests")
	_, revision, err = cache.GetSessionAccountState(s.ctx, 1, key)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.cache.DeleteSessionAccountID(s.ctx, 1, key))
	changed, err = cache.CompareAndSwapSessionAccountID(s.ctx, 1, key, revision, 8)
	require.NoError(s.T(), err)
	require.False(s.T(), changed)
	account, _, err = cache.GetSessionAccountState(s.ctx, 2, key)
	require.NoError(s.T(), err)
	require.Zero(s.T(), account, "another group must remain isolated")
}

func (s *GatewayCacheSuite) TestSessionAccountID_TTL() {
	sessionID := "s2"
	accountID := int64(100)
	groupID := int64(1)
	sessionTTL := 1 * time.Minute

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, sessionTTL), "SetSessionAccountID")

	sessionKey := buildSessionKey(groupID, sessionID)
	ttl, err := s.rdb.TTL(s.ctx, sessionKey).Result()
	require.NoError(s.T(), err, "TTL sessionKey after Set")
	s.AssertTTLWithin(ttl, 1*time.Second, sessionTTL)
}

func (s *GatewayCacheSuite) TestRefreshSessionTTL() {
	sessionID := "s3"
	accountID := int64(101)
	groupID := int64(1)
	initialTTL := 1 * time.Minute
	refreshTTL := 3 * time.Minute

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, initialTTL), "SetSessionAccountID")

	require.NoError(s.T(), s.cache.RefreshSessionTTL(s.ctx, groupID, sessionID, refreshTTL), "RefreshSessionTTL")

	sessionKey := buildSessionKey(groupID, sessionID)
	ttl, err := s.rdb.TTL(s.ctx, sessionKey).Result()
	require.NoError(s.T(), err, "TTL after Refresh")
	s.AssertTTLWithin(ttl, 1*time.Second, refreshTTL)
}

func (s *GatewayCacheSuite) TestRefreshSessionTTL_MissingKey() {
	// RefreshSessionTTL on a missing key should not error (no-op)
	err := s.cache.RefreshSessionTTL(s.ctx, 1, "missing-session", 1*time.Minute)
	require.NoError(s.T(), err, "RefreshSessionTTL on missing key should not error")
}

func (s *GatewayCacheSuite) TestDeleteSessionAccountID() {
	sessionID := "openai:s4"
	accountID := int64(102)
	groupID := int64(1)
	sessionTTL := 1 * time.Minute

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, sessionTTL), "SetSessionAccountID")
	require.NoError(s.T(), s.cache.DeleteSessionAccountID(s.ctx, groupID, sessionID), "DeleteSessionAccountID")

	_, err := s.cache.GetSessionAccountID(s.ctx, groupID, sessionID)
	require.True(s.T(), errors.Is(err, service.ErrStickySessionNotFound), "expected ErrStickySessionNotFound after delete")
}

func (s *GatewayCacheSuite) TestGetSessionAccountID_CorruptedValue() {
	sessionID := "corrupted"
	groupID := int64(1)
	sessionKey := buildSessionKey(groupID, sessionID)

	// Set a non-integer value
	require.NoError(s.T(), s.rdb.Set(s.ctx, sessionKey, "not-a-number", 1*time.Minute).Err(), "Set invalid value")

	_, err := s.cache.GetSessionAccountID(s.ctx, groupID, sessionID)
	require.Error(s.T(), err, "expected error for corrupted value")
	require.False(s.T(), errors.Is(err, service.ErrStickySessionNotFound), "expected parsing error, not a miss")
}

func TestGatewayCacheSuite(t *testing.T) {
	suite.Run(t, new(GatewayCacheSuite))
}
