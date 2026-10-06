---
title: Zoho Bigin OAuth Setup
description: Generate and renew the OAuth refresh token used by Bigin tools.
methods: []
depends_on:
  - internal/tools/bigin/client.go
used_by:
  - README.md
  - docs/tools.md
---

# Zoho Bigin OAuth setup

The bot uses a Zoho Bigin OAuth refresh token together with its existing client
ID and client secret. These values are configured as:

```text
TOOLS_BIGIN_REFRESH_TOKEN
TOOLS_BIGIN_CLIENT_ID
TOOLS_BIGIN_CLIENT_SECRET
```

## Renew the refresh token for Bigin emails

The Bigin Email related-list endpoint requires the
`ZohoBigin.modules.ALL` scope. Generate a new refresh token with that scope to
enable `get_bigin_deal_emails`.

1. Open the [Zoho API Console for the EU data centre](https://api-console.zoho.eu/).
2. Open the OAuth client already used by the bot, or create a **Self Client**.
3. Generate a grant token with this scope:

   ```text
   ZohoBigin.modules.ALL
   ```

4. Exchange the grant token for an offline refresh token through
   `https://accounts.zoho.eu/oauth/v2/token`, using the existing client ID and
   client secret.
5. Replace only `TOOLS_BIGIN_REFRESH_TOKEN` in the deployment secrets or local
   environment, then restart or redeploy the bot.

The existing client ID and client secret do not need to change. A token with
this scope also covers Bigin related-list reads, including the Emails related
list for a pipeline record, attachments read with
`download_bigin_deal_attachment`, and attachments uploaded with
`upload_bigin_deal_attachment`.

## Security notes

- Treat the client secret, grant token, access token, and refresh token as
  secrets; do not commit them or paste them in tickets or chat.
- Grant tokens are short-lived and single-use. Store only the resulting refresh
  token in `TOOLS_BIGIN_REFRESH_TOKEN`.
- The configured Zoho region is EU: use `api-console.zoho.eu`,
  `accounts.zoho.eu`, and `www.zohoapis.eu` consistently.
