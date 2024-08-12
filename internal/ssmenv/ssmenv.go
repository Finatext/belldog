package ssmenv

import (
	"context"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/cockroachdb/errors"
)

func ReplacedEnv(ctx context.Context, cli ssmClient, envs []string) (map[string]string, error) {
	orig := make(map[string]string)
	ssmKeys := []string{}

	for _, env := range envs {
		pair := strings.SplitN(env, "=", 2)
		if len(pair) != 2 {
			return nil, errors.Newf("invalid env var: %s", env)
		}
		key := pair[0]
		value := pair[1]
		orig[key] = value

		if strings.HasPrefix(value, ssmPrefix) {
			ssmKeys = append(ssmKeys, strings.TrimPrefix(value, ssmPrefix))
		}
	}

	if len(ssmKeys) == 0 {
		return orig, nil
	}

	slog.InfoContext(ctx, "fetching SSM parameters", slog.String("keys", strings.Join(ssmKeys, ",")))
	ps, err := batchFetch(ctx, cli, ssmKeys)
	if err != nil {
		return nil, err
	}
	for k, v := range orig {
		if strings.HasPrefix(v, ssmPrefix) {
			// Remove prefix, use strings.TrimPrefix
			key := strings.TrimPrefix(v, ssmPrefix)
			val, ok := ps[key]
			if !ok {
				return nil, errors.Newf("SSM parameter not found: %s", key)
			}

			orig[k] = val
		}
	}

	return orig, nil
}

const ssmPrefix = "ssm://"

type ssmClient interface {
	GetParameters(ctx context.Context, params *ssm.GetParametersInput, optFns ...func(*ssm.Options)) (*ssm.GetParametersOutput, error)
}

func batchFetch(ctx context.Context, cli ssmClient, keys []string) (map[string]string, error) {
	input := ssm.GetParametersInput{
		Names:          keys,
		WithDecryption: aws.Bool(true),
	}
	res, err := cli.GetParameters(ctx, &input)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get SSM parameters")
	}
	if len(res.InvalidParameters) > 0 {
		return nil, errors.Newf("invalid SSM parameters: %+v", res.InvalidParameters)
	}

	ret := make(map[string]string)
	for _, p := range res.Parameters {
		if p.Name == nil || p.Value == nil {
			return nil, errors.Newf("the SSM parameter has nil Name or Value: %+v", p)
		}
		ret[*p.Name] = *p.Value
	}
	return ret, nil
}
