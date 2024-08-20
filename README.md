Belldog
=======

Proxy webhook requests to Slack. Clarify webhook URLs to indicate target channel name. Simplify webhook issuing process and management.

## Usage
### Standard scenario
1. Issue new token and webhook URL in the target channel with the slash command "generate".
  1. To use in private channels, invite the bot user to the target channel.
1. Use the URL to post message to slack. The same arguments in `chat.postMessage` are supported. ref: https://api.slack.com/methods/chat.postMessage
  1. Note: the `channel` field will be add/overwrite by belldog. The proxy finds connected channel id from DB and uses it.

Example request:

```bash
curl -XPOST --json @hello.json 'https://<domain>/p/<channel_name>/<generated_token>/'
```

```json
{ "text": "hello" }
```

### Token migration
If token and URL are leaked, replace current token with new token and revoke the old token.

1. Generate another token in the target channel with "regenerate" command.
1. Replace URLs containing the old token.
1. All replace works are done, revoke the old token with "revoke" command.

### Channel name migration
1. Assume a token was generated in the old channel.
1. Rename the target channel. The webhook proxy must work as same as before. It uses channel id to post messages.
1. Generate new token in the renamed channel. And replace old URLs containing the old token with new one.
1. Batch job detects channel renaming and notify the pair of old channel name and its token.
1. Once all replace works are done, revoke the old token with special slash command "revoke renamed".
1. After revoking, the old channel name is safe to use by other channels. In other words, one can rename another channel to the old channel name.

## Setup and operation
### Mode
Belldog recommends 2 individual Lambda functions to work.

- `proxy` mode: Processes Slack slash commands and proxies webhook requests.
- `batch` mode: Detects token migrations and channel renamings and notify users and ops.

### Specification
- With standard "generate" command, only 1 token is valid for each channel (actually, channel name).
- With "regenerate" command, only 2 tokens are valid maximum for each channel (channel name). This is for token migration in case old token is leaked.
- Tokens are owned by the linked channel. One can revoke a token only in the channel in which the token had been generated.

### Environment Variables
Secrets should be stored at secure locations like AWS SSM Parameter Store. Use `ssm://<paramter_key>` as environment variable value to let Belldog
to retrive secret values from Parameter Store. The paramter key must contain the starting slash character (`/`).

- `DDB_TABLE_NAME`: DynamoDB table name.
- `MODE`: Switch proxy mode and batch mode in the start-up process.
- `OPS_NOTIFICATION_CHANNEL_NAME`: Slack channel name to notify token migrations and channel renamings to Ops.
- `SLACK_TOKEN`: Slack Bot User OAuth Token.
- `SLACK_SIGNING_SECRET`: https://api.slack.com/authentication/verifying-requests-from-slack

Deprecated:

- `PARAMETER_NAME_SLACK_TOKEN`: AWS SSM parameter store key to get Slack Bot User OAuth Token.
- `PARAMETER_NAME_SLACK_SIGNING_SECRET`: https://api.slack.com/authentication/verifying-requests-from-slack

Optional:

- `CUSTOM_DOMAIN_NAME`: Custom domain name to be used to reach to Belldog instance. If omitted, host/authority HTTP field will be used.

### Slack permissions
See `./example_app_manifest.yaml` to use Slack App Manifest.

Posting messages:

- `chat:write.public`: To post not-invited channels.
- `chat:write`: Required by chat:write.public.
- `groups:write`: To post invited private channels.

Slash command:

- `commands`: To enable slash command.

Batch job to find in migration tokens:

- `channels:read`: To list all public channels.
- `groups:read`: To list all private channels which belldog is in.

Optional:

- `chat:write.customize`: Post message as other entities.

### Slack slash commands
See `./example_app_manifest.yaml` to use Slack App Manifest.

Endpoint is `<base_url>/slash/` (requires tail slash).

- `/belldog-show`: "Show all tokens connected to this channel.", no hint
- `/belldog-generate`: "Generate token and webhook URL.", no hint
- `/belldog-regenerate`: "Regenerate another token and URL.", no hint
- `/belldog-revoke`: "Revoke token. Only available in the channel in which the token was generated.", hint "<token>"
- `/belldog-revoke-renamed`: "Revoke old token. Use this after channel name renamed.", hint "<old channel name> <token>"

### IAM permissions
- Basic Lambda execution permissions
- DynamoDB's Query, PutItem, DeleteItem, Scan
- SSM's GetParameter

### DynamoDB table
- Partition key: `channel_name` string
- Sort key: `version` number

Estimate average item size: 100-150 bytes.

### Lambda instruction set architecture
Currently only `x86_64` architecture is supported.

## Development
### Upgrade Go version
- `go.mod`
- `Dockerfile`
