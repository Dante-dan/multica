# Matrix transport (not yet enabled)

This package is the plaintext native transport slice of issue #9086. It
implements the existing `channel.Channel` contract without changing the
channel engine or adding an SDK dependency. No production factory registration,
installation route, identity-binding route or outbound event-bus wiring is
included yet, so it does not expose a working Matrix integration in Multica.

A caller supplies the homeserver, stable Matrix user/device IDs, and a bot
access token. `Factory` requires encrypted token storage and an injected
`CursorStore` for inbound connections. Handler success must mean the engine's
deduplicated enqueue is durable before the cursor is saved. The engine rebuilds
adapters on reconnect; an in-memory cursor alone is insufficient.

The first `/sync` establishes a baseline without executing historical messages.
Later batches use the saved token, backfill limited timelines, normalize only
`m.text` messages, and advance the cursor only after all handlers succeed.
Every room is one session key; Matrix threads do not split that key. Structured
`m.mentions.user_ids` addresses the bot. `require_mention=false` opts all room
messages into the downstream router; rooms in the bot account's `m.direct`
mapping do not require mentions. Membership, authorization and identity checks
remain responsibilities of the existing engine; a Matrix sender must never be
implicitly mapped to the installation's owner.

Text replies use `m.notice` to avoid other bots answering them. Edits are
`m.replace` events and typing uses the Client-Server typing endpoint. HTTP 429
retries prefer `Retry-After`, support legacy `retry_after_ms`, obey cancellation
and retain one transaction ID. Caller-level delivery retries still need an
installation delivery ledger with stable transaction IDs. Credentials are
never placed in URLs or server-error logs; redirects are rejected.

Encrypted rooms are excluded from receive, and outbound verifies encryption
state before sending. E2EE, media, auto-joining invitations, persistence/wiring,
stream scheduling, legacy HTML mentions, and a complete operator UI are not
implemented by this slice. It has not been exercised against real Synapse or
Element; mock transport tests must not be described as that validation.

Protocol sources:

- https://spec.matrix.org/latest/client-server-api/#get_matrixclientv3sync
- https://spec.matrix.org/latest/client-server-api/#user-and-room-mentions
- https://spec.matrix.org/latest/client-server-api/#direct-messaging
- https://spec.matrix.org/latest/client-server-api/#rate-limiting
- https://spec.matrix.org/latest/client-server-api/#event-replacements
- https://spec.matrix.org/latest/client-server-api/#mroomencrypted
