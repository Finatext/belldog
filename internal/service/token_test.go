package service

import (
	"context"
	"testing"

	"github.com/cockroachdb/errors"

	"github.com/Finatext/belldog/internal/storage"
)

type testStorage struct {
	m map[string][]storage.Record
}

func newTestStorage() testStorage {
	m := make(map[string][]storage.Record)
	return testStorage{m: m}
}

func (t *testStorage) Save(ctx context.Context, rec storage.Record) error {
	t.m[rec.ChannelName] = append(t.m[rec.ChannelName], rec)
	return nil
}

func (t *testStorage) QueryByChannelName(ctx context.Context, channelName string) ([]storage.Record, error) {
	recs, ok := t.m[channelName]
	if !ok {
		return []storage.Record{}, nil
	}
	return recs, nil
}

func (t *testStorage) Delete(ctx context.Context, rec storage.Record) error {
	recs, ok := t.m[rec.ChannelName]
	if !ok {
		return errors.Newf("No record found for %s", rec.ChannelName)
	}

	for i, v := range recs {
		if v.ChannelName == rec.ChannelName && v.Token == rec.Token {
			recs[i] = recs[len(recs)-1]
			t.m[rec.ChannelName] = recs[:len(recs)-1]
			return nil
		}
	}
	return nil
}

const (
	channelID          = "C03T4AU1755"
	channelName        = "random"
	anotherChannelName = "general"
	token              = "test token"
)

func TestGenerateAndSaveTokenNew(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stg := newTestStorage()
	svc := NewTokenService(&stg)

	res, err := svc.GenerateAndSaveToken(ctx, channelID, channelName)
	if err != nil {
		t.Fatalf("GenerateAndSaveToken failed: %s", err)
	}
	recs := stg.m[channelName]
	if len(recs) == 0 {
		t.FailNow()
	}
	rec := recs[0]
	if res.Token != rec.Token {
		t.Fatalf("Returned token and saved token unmatch: returned=%s, saved=%s", res.Token, rec.Token)
	}
	if !res.IsGenerated {
		t.Fatal("Token is not newly generated")
	}
}

func TestGenerateAndSaveTokenAgain(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stg := newTestStorage()
	svc := NewTokenService(&stg)

	resOld, err := svc.GenerateAndSaveToken(ctx, channelID, channelName)
	if err != nil {
		t.Fatalf("GenerateAndSaveToken failed: %s", err)
	}
	token := resOld.Token
	// GenerateAgain
	res, err := svc.GenerateAndSaveToken(ctx, channelID, channelName)
	if err != nil {
		t.Fatalf("GenerateAndSaveToken failed: %s", err)
	}
	if res.Token != token {
		t.Fatalf("Returned token doesn't match to previously generated token: returned=%s, previous=%s", res.Token, token)
	}
	if res.IsGenerated {
		t.Fatal("Token is not previously generated")
	}
	if len(stg.m[channelName]) != 1 {
		t.FailNow()
	}
}

func TestVerifyToken(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stg := newTestStorage()
	svc := NewTokenService(&stg)

	rec := storage.Record{ChannelID: channelID, ChannelName: channelName, Token: token, Version: 1}
	if err := stg.Save(ctx, rec); err != nil {
		t.Fatalf("Failed to save record: %s", err)
	}

	res1, err := svc.VerifyToken(ctx, anotherChannelName, token)
	if err != nil {
		t.Fatalf("VerifyToken failed: %s", err)
	}
	if !res1.NotFound {
		t.Fatal("VerifyResult.NotFound must be true")
	}

	res2, err := svc.VerifyToken(ctx, channelName, "invalid token")
	if err != nil {
		t.Fatalf("VerifyToken failed: %s", err)
	}
	if res2.NotFound {
		t.Fatal("VerifyResult.NotFound must be false")
	}
	if !res2.Unmatch {
		t.Fatal("VerifyResult.Unmatch must be true")
	}

	res3, err := svc.VerifyToken(ctx, channelName, token)
	if err != nil {
		t.Fatalf("VerifyToken failed: %s", err)
	}
	if res3.NotFound {
		t.Fatal("VerifyResult.NotFound must be false")
	}
	if res3.Unmatch {
		t.Fatal("VerifyResult.Unmatch must be false")
	}
	if res3.ChannelID != channelID {
		t.Fatal("Saved ChannelID and channelID must be same")
	}
}

func TestVerifyTokenMultipleItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stg := newTestStorage()
	svc := NewTokenService(&stg)

	rec := storage.Record{ChannelID: channelID, ChannelName: channelName, Token: token, Version: 1}
	if err := stg.Save(ctx, rec); err != nil {
		t.Fatalf("Failed to save record: %s", err)
	}
	token2 := "test token 2"
	rec2 := storage.Record{ChannelID: channelID, ChannelName: channelName, Token: token2, Version: 2}
	if err := stg.Save(ctx, rec2); err != nil {
		t.Fatalf("Failed to save record: %s", err)
	}

	res1, err := svc.VerifyToken(ctx, channelName, token2)
	if err != nil {
		t.Fatalf("VerifyToken failed: %s", err)
	}
	if res1.NotFound {
		t.Fatal("VerifyResult.NotFound must be false")
	}
	if res1.Unmatch {
		t.Fatal("VerifyResult.Unmatch must be false")
	}
	if res1.ChannelID != channelID {
		t.Fatalf("Saved ChannelID and channelID must be same: saved=%s, init=%s, rec=%v", res1.ChannelID, channelID, res1)
	}
}

func TestRegenerateToken(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stg := newTestStorage()
	svc := NewTokenService(&stg)

	// Case: no token saved.
	res1, err := svc.RegenerateToken(ctx, channelID, channelName)
	if err != nil {
		t.Fatalf("Failed to RegenerateToken: %s", err)
	}
	if !res1.NoTokenFound {
		t.FailNow()
	}

	// Case: regular pass.
	rec := storage.Record{ChannelID: channelID, ChannelName: channelName, Token: token, Version: 0}
	if err := stg.Save(ctx, rec); err != nil {
		t.Fatalf("Failed to save record: %s", err)
	}
	res2, err := svc.RegenerateToken(ctx, channelID, channelName)
	if err != nil {
		t.Fatalf("Failed to RegenerateToken: %s", err)
	}
	if res2.NoTokenFound {
		t.FailNow()
	}
	if res2.TooManyToken {
		t.FailNow()
	}
	if res2.Token == rec.Token {
		t.Fatalf("NewToken has same value as existing token: token=%s", rec.Token)
	}
	recs, ok := stg.m[channelName]
	if !ok {
		t.FailNow()
	}
	if len(recs) != 2 {
		t.Fatalf("Not enoght recs found: %v", recs)
	}
	if recs[0].Version != 0 {
		t.FailNow()
	}
	if recs[1].Version != 1 {
		t.FailNow()
	}

	// Case: too many token.
	res3, err := svc.RegenerateToken(ctx, channelID, channelName)
	if err != nil {
		t.Fatalf("Failed to RegenerateToken: %s", err)
	}
	if !res3.TooManyToken {
		t.FailNow()
	}
}

func TestRevokeToken(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stg := newTestStorage()
	svc := NewTokenService(&stg)

	res, err := svc.RevokeToken(ctx, channelName, token)
	if err != nil {
		t.Fatalf("RevokeToken failed: %s", err)
	}
	if !res.NotFound {
		t.FailNow()
	}

	rec := storage.Record{ChannelID: channelID, ChannelName: channelName, Token: token, Version: 1}
	if err := stg.Save(ctx, rec); err != nil {
		t.Fatalf("Failed to save record: %s", err)
	}
	res2, err := svc.RevokeToken(ctx, channelName, token)
	if err != nil {
		t.Fatalf("RevokeToken failed: %s", err)
	}
	if res2.NotFound {
		t.FailNow()
	}
}

const sameToken = "same token"

type testGenerator struct{}

func (g *testGenerator) generate() (string, error) {
	return sameToken, nil
}

func TestGenerateWithRetry(t *testing.T) {
	t.Parallel()

	recs := []storage.Record{
		{Token: token},
		{Token: "another token"},
	}

	gen := testGenerator{}
	if _, err := generateWithRetry(recs, &gen); err != nil {
		t.FailNow()
	}

	recs = append(recs, storage.Record{Token: sameToken})
	if _, err := generateWithRetry(recs, &gen); err == nil {
		t.Fatal("generateWithRetry must return an error when same token found continuously.")
	}
}
