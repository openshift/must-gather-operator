#!/usr/bin/env bash
# Generate a short-lived SFTP username/token pair via Red Hat stage APIs.
# Used by e2e when getCaseCredsFromVault runs this script with the offline token in RH_OFFLINE_TOKEN (see must_gather_operator_test.go).
# Local runs typically use SFTP_USERNAME_E2E and SFTP_PASSWORD_E2E instead.
#
# Dependencies: curl, jq
#
# Environment:
#   RH_OFFLINE_TOKEN  SSO offline refresh token (required for this script). The Go test passes it when the token
#                     was read from CASE_MANAGEMENT_CREDS_CONFIG_DIR (Vault); you do not need to export it locally
#                     if you use SFTP_USERNAME_E2E and SFTP_PASSWORD_E2E instead.
#   STAGE_PROXY_HOST  HTTP proxy host:port for curl (default: squid.corp.redhat.com:3128). Empty disables -x.
#
# stdout: single JSON object {"username":"<user>","password":"<token>"} for the e2e harness.
# stderr: diagnostics only.

set -euo pipefail

STAGE_PROXY_HOST="${STAGE_PROXY_HOST:-squid.corp.redhat.com:3128}"

if [[ -z "${RH_OFFLINE_TOKEN:-}" ]]; then
	echo "error: RH_OFFLINE_TOKEN is required" >&2
	exit 1
fi
offline_token="${RH_OFFLINE_TOKEN}"

curl_proxy=()
if [[ -n "${STAGE_PROXY_HOST}" ]]; then
	curl_proxy=(-x "${STAGE_PROXY_HOST}")
fi

token_json=$(curl -sS "${curl_proxy[@]}" \
	https://sso.stage.redhat.com/auth/realms/redhat-external/protocol/openid-connect/token \
	-d grant_type=refresh_token \
	-d client_id=rhsm-api \
	-d refresh_token="${offline_token}")

access_token=$(echo "${token_json}" | jq --raw-output '.access_token // empty')
if [[ -z "${access_token}" || "${access_token}" == "null" ]]; then
	echo "error: failed to obtain access_token from SSO (check refresh token and proxy)" >&2
	echo "${token_json}" | jq -r '.error_description // .error // .' >&2 || echo "${token_json}" >&2
	exit 1
fi

sftp_json=$(curl -sS "${curl_proxy[@]}" \
	-H "Authorization: Bearer ${access_token}" \
	-H 'Accept: application/json' \
	-H 'Content-Type: application/json' \
	-X POST \
	--data '{"isAnonymous":false,"isOneTime":false,"expiryInDays":90}' \
	'https://api.access.stage.redhat.com/support/v2/sftp/token')

username=$(echo "${sftp_json}" | jq --raw-output '.username // empty')
password=$(echo "${sftp_json}" | jq --raw-output '.token // empty')

if [[ -z "${username}" || -z "${password}" ]]; then
	echo "error: unexpected SFTP token response" >&2
	echo "${sftp_json}" | jq . >&2 2>/dev/null || echo "${sftp_json}" >&2
	exit 1
fi

jq -n --arg u "${username}" --arg p "${password}" '{username: $u, password: $p}'
