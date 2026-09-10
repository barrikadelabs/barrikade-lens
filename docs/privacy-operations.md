# Privacy operations for managed Lens

This runbook covers authenticated access and erasure requests for the production design-partner service. It must be performed only by the designated Barrikade privacy administrator. Never copy raw Clerk or workspace identifiers into PostHog, GitHub, chat, or an external support ticket.

## Verify the request

1. Receive the request through `ishaan@barrikade.ai` or the approved partner channel.
2. Verify the requester's active Clerk identity and their authority over each requested workspace.
3. Record only the internal request reference, scope, verifier, and timestamps in the restricted privacy log. Do not record discovery data or credentials.
4. Classify the scope as user-attributed analytics, one or more workspaces, application data, or all authorized data. A user request does not automatically authorize deletion of actorless workspace events owned by the partner organization.

## Locate PostHog pseudonyms

Use the production `posthog-id-salt` value from the Container App secret and the raw identifiers from the authenticated Lens session. Keep the salt in an ephemeral environment variable. The helper reads the raw identifier from stdin so it is not placed in shell history:

```bash
read -rs LENS_POSTHOG_ID_SALT
export LENS_POSTHOG_ID_SALT
read -r lens_identifier
printf '%s\n' "$lens_identifier" | go run ./cmd/lens-analytics-admin --kind user
unset lens_identifier LENS_POSTHOG_ID_SALT
```

Use `--kind workspace` for each authorized raw workspace ID. The output must begin with `phu_` for a user or `phw_` for a workspace. Use only the pseudonym in PostHog searches and deletion calls.

## Delete and verify

1. In PostHog EU project `269284`, find the exact pseudonym. Confirm every returned event has `deployment_environment = production` before deletion.
2. Create or use a time-bounded PostHog personal API key with only `person:read` and `person:write`. Keep it in an ephemeral `POSTHOG_PERSONAL_API_KEY` environment variable and revoke it immediately after the operation if it was created for the request.
3. Production deliberately uses `person_profiles: never`, so a pseudonym can have events without a profile page. Queue event and recording deletion through the bulk endpoint by exact distinct ID:

   ```bash
   read -rs POSTHOG_PERSONAL_API_KEY
   export POSTHOG_PERSONAL_API_KEY
   read -r lens_posthog_id
   jq -n --arg id "$lens_posthog_id" \
     '{distinct_ids:[$id],delete_events:true,delete_recordings:true,keep_person:false}' |
     curl --fail-with-body --silent --show-error \
       -X POST 'https://eu.posthog.com/api/projects/269284/persons/bulk_delete/' \
       -H "Authorization: Bearer $POSTHOG_PERSONAL_API_KEY" \
       -H 'Content-Type: application/json' \
       --data-binary @-
   unset lens_posthog_id POSTHOG_PERSONAL_API_KEY
   ```

   The response must report that the identity was found and event deletion was queued. For a workspace-scoped request, delete the `phw_` identity only after workspace authority has been verified.
4. Record the deletion request time and its non-sensitive operation identifier. Event deletion is asynchronous; do not report completion merely because the identity no longer appears in the UI.
5. Check `GET /api/projects/269284/persons/deletion_status/` with the same scoped key until it reports `completed`, then repeat the exact pseudonym search and confirm that no events or recordings remain.
6. Delete or de-identify the authorized Lens application data, Clerk identity, and support correspondence as required by the request. Account Settings can delete an identity or an entire workspace, subject to the sole-owner rule.
7. Record completion, verifier, systems checked, and exceptions in the restricted privacy log. Tell the requester what was deleted, what was retained, why, and when the work completed.

Do not rotate the production salt to perform erasure. Rotation prevents reliable lookup without deleting retained data. Do not use a staging salt, token, dashboard, or project for a production request.

## Access review

Review access quarterly and after personnel changes:

- Azure and production database: service operation and incident response only.
- Clerk: account support and identity administration only.
- PostHog project, deletion permission, and production pseudonym salt: designated privacy administrator only; product collaborators receive the minimum read access needed for approved aggregated insights.
- Zoho support mailbox and restricted privacy log: personnel handling partner support or privacy requests only.
- GitHub: source and deployment operations; raw partner data and PostHog payloads are prohibited.

## Retention

- PostHog session recordings: 30 days, enforced in the EU project.
- Pseudonymous PostHog product events: 12 months maximum. Review and reduce this period when PostHog plan controls permit.
- Application data: duration of the design-partner relationship, then deletion or de-identification when no longer needed, subject to security, backup, dispute, and legal obligations.
- Support and privacy correspondence: only while needed to resolve the request and meet operational or legal obligations.

The public notice at `/privacy` is authoritative for design partners. Update it before materially expanding collection, subprocessors, purposes, or retention.
