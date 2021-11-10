#!/usr/bin/env bash
# Sets up vault authn
set -e

if [[ -n $VAULT_ROLE_ID ]] && [[ -n $VAULT_SECRET_ID ]]; then
  # TODO(jaredallard): Put in box configuration. Need to build support for that.
  VAULT_ADDR=https://vault-dev.outreach.cloud vault write auth/approle/login \
    role_id="$VAULT_ROLE_ID" secret_id="$VAULT_SECRET_ID" -format=json |
    jq .auth.client_token -r >~/.vault-token
else
  echo "Skipping, no VAULT_ROLE_ID or VAULT_SECRET_ID set"
fi