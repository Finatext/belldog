package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"fmt"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/Finatext/belldog/internal/storage"
)

// TODO: Remove this extra layer, merge this to storage.Record.
type Entry struct {
	Token     string
	Version   int
	CreatedAt time.Time
}

type VerifyResult struct {
	NotFound    bool
	Unmatch     bool
	ChannelID   string
	ChannelName string
}

type GenerateResult struct {
	IsGenerated bool
	Token       string
}

type RegenerateResult struct {
	NoTokenFound bool
	TooManyToken bool
	Token        string
}

type RevokeResult struct {
	NotFound bool
}

type RevokeRenamedResult struct {
	NotFound         bool
	ChannelIDUnmatch bool
	LinkedChannelID  string
}

type TokenService struct {
	ddb ddb
}

func NewTokenService(ddb ddb) TokenService {
	return TokenService{ddb: ddb}
}

func (d *TokenService) GetTokens(ctx context.Context, channelName string) ([]Entry, error) {
	recs, err := d.ddb.QueryByChannelName(ctx, channelName)
	if err != nil {
		return []Entry{}, err
	}
	entries := make([]Entry, 0, len(recs))
	for _, rec := range recs {
		e, err := recordToEntry(rec)
		if err != nil {
			return []Entry{}, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// VerifyToken checks given token and existin token. It returns VerifyResult.
// Need to check the returned VerifyResult.NotFound and .Unmatch.
// Returns an error when underlying storage goes wrong.
func (d *TokenService) VerifyToken(ctx context.Context, channelName string, givenToken string) (VerifyResult, error) {
	recs, err := d.ddb.QueryByChannelName(ctx, channelName)
	if err != nil {
		return VerifyResult{}, err
	}
	if len(recs) == 0 {
		return VerifyResult{NotFound: true}, nil
	}

	for _, rec := range recs {
		existingToken := rec.Token
		res := hmac.Equal([]byte(existingToken), []byte(givenToken))
		if res {
			return VerifyResult{NotFound: false, ChannelID: rec.ChannelID, ChannelName: rec.ChannelName}, nil
		}
	}
	return VerifyResult{Unmatch: true}, nil
}

// GenerateAndSaveToken returns a GenerateResult which contains secure random string as token.
// Then it saves the generated token to storage. This checks existing generated token in storage.
// If found, returns the generated token.
func (d *TokenService) GenerateAndSaveToken(ctx context.Context, channelID string, channelName string) (GenerateResult, error) {
	recs, err := d.ddb.QueryByChannelName(ctx, channelName)
	if err != nil {
		return GenerateResult{}, err
	}
	if len(recs) > 0 {
		rec := recs[0]
		res := GenerateResult{IsGenerated: false, Token: rec.Token}
		return res, nil
	}

	gen := generatorImpl{}
	token, err := gen.generate()
	if err != nil {
		return GenerateResult{}, err
	}

	record := storage.Record{
		ChannelID:   channelID,
		ChannelName: channelName,
		Token:       token,
		Version:     0,
		CreatedAt:   currentTimestamp(),
	}
	if err := d.ddb.Save(ctx, record); err != nil {
		return GenerateResult{}, err
	}

	res := GenerateResult{IsGenerated: true, Token: token}
	return res, nil
}

const maxTokenCount = 2

// RegenerateToken allows generate another token for the given channel. If another
// token has been already generated, it returns "too many token" result. So users
// can have 2 tokens for each channel name maximum.
func (d *TokenService) RegenerateToken(ctx context.Context, channelID string, channelName string) (RegenerateResult, error) {
	recs, err := d.ddb.QueryByChannelName(ctx, channelName)
	if err != nil {
		return RegenerateResult{}, err
	}
	if len(recs) == 0 {
		return RegenerateResult{NoTokenFound: true}, nil
	}
	if len(recs) >= maxTokenCount {
		return RegenerateResult{TooManyToken: true}, nil
	}

	gen := generatorImpl{}
	token, err := generateWithRetry(recs, &gen)
	if err != nil {
		return RegenerateResult{}, errors.Wrapf(err, "same token generated: token=%s", token)
	}

	// QueryByChannelName returns sorted records.
	latestRec := recs[0]
	record := storage.Record{
		ChannelID:   channelID,
		ChannelName: channelName,
		Token:       token,
		Version:     latestRec.Version + 1,
		CreatedAt:   currentTimestamp(),
	}
	if err := d.ddb.Save(ctx, record); err != nil {
		return RegenerateResult{}, err
	}
	return RegenerateResult{Token: token}, nil
}

func (d *TokenService) RevokeToken(ctx context.Context, channelName string, givenToken string) (RevokeResult, error) {
	recs, err := d.ddb.QueryByChannelName(ctx, channelName)
	if err != nil {
		return RevokeResult{}, err
	}
	if len(recs) == 0 {
		return RevokeResult{NotFound: true}, nil
	}

	for _, rec := range recs {
		if rec.Token == givenToken {
			if err := d.ddb.Delete(ctx, rec); err != nil {
				return RevokeResult{}, err
			}
			// Success.
			return RevokeResult{}, nil
		}
	}
	return RevokeResult{NotFound: true}, nil
}

// Revoke given token for the given channel name. If then token is not linked to another channel's id, treat as permission error.
func (d *TokenService) RevokeRenamedToken(ctx context.Context, channelID string, givenChannelName string, givenToken string) (RevokeRenamedResult, error) {
	recs, err := d.ddb.QueryByChannelName(ctx, givenChannelName)
	if err != nil {
		return RevokeRenamedResult{}, err
	}
	if len(recs) == 0 {
		return RevokeRenamedResult{NotFound: true}, nil
	}

	for _, rec := range recs {
		if rec.Token == givenToken {
			if rec.ChannelID != channelID {
				return RevokeRenamedResult{ChannelIDUnmatch: true, LinkedChannelID: rec.ChannelID}, nil
			}

			if err := d.ddb.Delete(ctx, rec); err != nil {
				return RevokeRenamedResult{}, err
			}
			// Success.
			return RevokeRenamedResult{}, nil
		}
	}
	return RevokeRenamedResult{NotFound: true}, nil
}

type ddb interface {
	Save(ctx context.Context, record storage.Record) error
	// QueryByChannelName returns found records having the same channel name.
	// It returns empty slice when no record found.
	QueryByChannelName(ctx context.Context, channelName string) ([]storage.Record, error)
	Delete(ctx context.Context, record storage.Record) error
}

type generator interface {
	generate() (string, error)
}

type generatorImpl struct{}

const randomStringLen = 16

func (g *generatorImpl) generate() (string, error) {
	k := make([]byte, randomStringLen)
	if _, err := rand.Read(k); err != nil {
		return "", errors.Wrap(err, "failed to generate random string")
	}
	return fmt.Sprintf("%x", k), nil
}

func generateWithRetry(recs []storage.Record, gen generator) (string, error) {
	for i := 0; i <= 3; i++ {
		pass := true

		token, err := gen.generate()
		if err != nil {
			return "", errors.Wrap(err, "failed to generate token")
		}
		for _, rec := range recs {
			if token == rec.Token {
				pass = false
				break
			}
		}
		if pass {
			return token, nil
		}
	}

	return "", errors.New("generate token 3 times but same token generated")
}

func recordToEntry(rec storage.Record) (Entry, error) {
	t, err := time.Parse(time.RFC3339Nano, rec.CreatedAt)
	if err != nil {
		return Entry{}, errors.Wrapf(err, "failed to parse created_at: %s", rec.CreatedAt)
	}
	return Entry{Token: rec.Token, Version: rec.Version, CreatedAt: t}, nil
}

func currentTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
