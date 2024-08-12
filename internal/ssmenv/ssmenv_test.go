package ssmenv

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type mockSsmClient struct {
	mock.Mock
}

func (m *mockSsmClient) GetParameters(ctx context.Context, params *ssm.GetParametersInput, optFns ...func(*ssm.Options)) (*ssm.GetParametersOutput, error) {
	args := m.Called(ctx, params)
	return args.Get(0).(*ssm.GetParametersOutput), args.Error(1)
}

func TestReplacedEnv(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cli := &mockSsmClient{}
	envs := []string{"AAA=ssm://path_to_parameter", "BBB=value"}
	cli.On("GetParameters", ctx, &ssm.GetParametersInput{
		Names:          []string{"path_to_parameter"},
		WithDecryption: aws.Bool(true),
	}).Return(&ssm.GetParametersOutput{
		Parameters: []types.Parameter{
			{
				Name:  aws.String("path_to_parameter"),
				Value: aws.String("ssmValue"),
			},
		},
	}, nil)

	actual, err := ReplacedEnv(ctx, cli, envs)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"AAA": "ssmValue", "BBB": "value"}, actual)
}
