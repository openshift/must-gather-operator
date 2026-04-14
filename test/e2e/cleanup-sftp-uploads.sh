#!/usr/bin/env bash
# Removes <E2E_SFTP_CASE_ID>_must-gather-*.tar.gz on Red Hat SFTP (external: cwd; internal: under $SFTP_USERNAME/).
# Invoked from cleanupSFTPUploadsByCaseID via bash -c; expects E2E_SFTP_* env, SFTP_USERNAME, SSHPASS from the pod.
set -euo pipefail

: "${E2E_SFTP_CASE_ID:?}"
: "${E2E_SFTP_HOST:?}"
: "${SFTP_USERNAME:?}"

SSH_DIR=/tmp/.ssh
mkdir -p "$SSH_DIR"
touch "$SSH_DIR/known_hosts"
chmod 700 "$SSH_DIR"
chmod 600 "$SSH_DIR/known_hosts"

sftp_ssh() {
	sshpass -e sftp -o BatchMode=no -o StrictHostKeyChecking=no \
		-o "UserKnownHostsFile=${SSH_DIR}/known_hosts" "$@"
}

list_path=.
if [[ "${E2E_SFTP_INTERNAL:-}" == "true" ]]; then
	list_path=$SFTP_USERNAME
fi

LISTING_FILE=/tmp/sftp-ls.txt
printf 'ls -la %s\nbye\n' "$list_path" | sftp_ssh "${SFTP_USERNAME}@${E2E_SFTP_HOST}" >"$LISTING_FILE" 2>&1

files=$(grep "${E2E_SFTP_CASE_ID}_must-gather" <"$LISTING_FILE" | awk '{print $NF}' | grep '\.tar\.gz$' || true)
if [[ -z "${files}" ]]; then
	echo "No matching SFTP files for case ${E2E_SFTP_CASE_ID}; listing follows:"
	cat "$LISTING_FILE" || true
	exit 0
fi

rm_script=$(mktemp)
while IFS= read -r fn; do
	[[ -z "$fn" ]] && continue
	if [[ "${E2E_SFTP_INTERNAL:-}" == "true" ]]; then
		printf 'rm %s/%s\n' "$SFTP_USERNAME" "$fn" >>"$rm_script"
	else
		printf 'rm %s\n' "$fn" >>"$rm_script"
	fi
done <<<"$files"
printf 'bye\n' >>"$rm_script"
sftp_ssh -b "$rm_script" "${SFTP_USERNAME}@${E2E_SFTP_HOST}"
rm -f "$rm_script"
echo "SFTP cleanup done for case ${E2E_SFTP_CASE_ID}"
