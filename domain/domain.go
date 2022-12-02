package domain

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/Finatext/belldog/storage"
)

type Storage interface {
	Save(ctx context.Context, record storage.Record) error
	// QueryByChannelName returns found records having the same channel name.
	// It returns empty slice when no record found.
	QueryByChannelName(ctx context.Context, channelName string) ([]storage.Record, error)
	Delete(ctx context.Context, record storage.Record) error
}

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

type Domain struct {
	st Storage
}

func NewDomain(st Storage) Domain {
	return Domain{st: st}
}

func (d *Domain) GetTokens(ctx context.Context, channelName string) ([]Entry, error) {
	recs, err := d.st.QueryByChannelName(ctx, channelName)
	if err != nil {
		return []Entry{}, fmt.Errorf("QueryByChannelName failed: %w", err)
	}
	entries := make([]Entry, 0, len(recs))
	for _, rec := range recs {
		e, err := recordToEntry(rec)
		if err != nil {
			return []Entry{}, fmt.Errorf("recordToEntry failed: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// VerifyToken checks given token and existin token. It returns VerifyResult.
// Need to check the returned VerifyResult.NotFound and .Unmatch.
// Returns an error when underlying storage goes wrong.
func (d *Domain) VerifyToken(ctx context.Context, channelName string, givenToken string) (VerifyResult, error) {
	recs, err := d.st.QueryByChannelName(ctx, channelName)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("QueryByChannelName failed: %w", err)
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
func (d *Domain) GenerateAndSaveToken(ctx context.Context, channelID string, channelName string) (GenerateResult, error) {
	recs, err := d.st.QueryByChannelName(ctx, channelName)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("QueryByChannelName failed: %w", err)
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
	if err := d.st.Save(ctx, record); err != nil {
		return GenerateResult{}, fmt.Errorf("storage.Save failed: %w", err)
	}

	res := GenerateResult{IsGenerated: true, Token: token}
	return res, nil
}

const maxTokenCount = 2

// RegenerateToken allows generate another token for the given channel. If another
// token has been already generated, it returns "too many token" result. So users
// can have 2 tokens for each channel name maximum.
func (d *Domain) RegenerateToken(ctx context.Context, channelID string, channelName string) (RegenerateResult, error) {
	recs, err := d.st.QueryByChannelName(ctx, channelName)
	if err != nil {
		return RegenerateResult{}, fmt.Errorf("QueryByChannelName failed: %w", err)
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
		return RegenerateResult{}, fmt.Errorf("same token generated: token=%s", token)
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
	if err := d.st.Save(ctx, record); err != nil {
		return RegenerateResult{}, fmt.Errorf("storage.Save failed: %w", err)
	}
	return RegenerateResult{Token: token}, nil
}

func (d *Domain) RevokeToken(ctx context.Context, channelName string, givenToken string) (RevokeResult, error) {
	recs, err := d.st.QueryByChannelName(ctx, channelName)
	if err != nil {
		return RevokeResult{}, fmt.Errorf("QueryByChannelName failed: %w", err)
	}
	if len(recs) == 0 {
		return RevokeResult{NotFound: true}, nil
	}

	for _, rec := range recs {
		if rec.Token == givenToken {
			if err := d.st.Delete(ctx, rec); err != nil {
				return RevokeResult{}, fmt.Errorf("Storage.Delete failed: %w", err)
			}
			// Success.
			return RevokeResult{}, nil
		}
	}
	return RevokeResult{NotFound: true}, nil
}

// Revoke given token for the given channel name. If then token is not linked to another channel's id, treat as permission error.
func (d *Domain) RevokeRenamedToken(ctx context.Context, channelID string, givenChannelName string, givenToken string) (RevokeRenamedResult, error) {
	recs, err := d.st.QueryByChannelName(ctx, givenChannelName)
	if err != nil {
		return RevokeRenamedResult{}, fmt.Errorf("QueryByChannelName failed: %w", err)
	}
	if len(recs) == 0 {
		return RevokeRenamedResult{NotFound: true}, nil
	}

	for _, rec := range recs {
		if rec.Token == givenToken {
			if rec.ChannelID != channelID {
				return RevokeRenamedResult{ChannelIDUnmatch: true, LinkedChannelID: rec.ChannelID}, nil
			}

			if err := d.st.Delete(ctx, rec); err != nil {
				return RevokeRenamedResult{}, fmt.Errorf("Storage.Delete failed: %w", err)
			}
			// Success.
			return RevokeRenamedResult{}, nil
		}
	}
	return RevokeRenamedResult{NotFound: true}, nil
}

type generator interface {
	generate() (string, error)
}

type generatorImpl struct{}

const randomStringLen = 16

func (g *generatorImpl) generate() (string, error) {
	k := make([]byte, randomStringLen)
	if _, err := rand.Read(k); err != nil {
		return "", fmt.Errorf("rand.Read failed: %w", err)
	}
	return fmt.Sprintf("%x", k), nil
}

func generateWithRetry(recs []storage.Record, gen generator) (string, error) {
	for i := 0; i <= 3; i++ {
		pass := true

		token, err := gen.generate()
		if err != nil {
			return "", fmt.Errorf("generator.generate failed: %w", err)
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
		return Entry{}, fmt.Errorf("time.Parse failed: %w", err)
	}
	return Entry{Token: rec.Token, Version: rec.Version, CreatedAt: t}, nil
}

func currentTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
