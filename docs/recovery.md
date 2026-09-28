# Event data and server recovery

Each client keeps the tickets, baskets and drawing entries saved through that client. The server combines entries from all clients. Online pages can read the combined event from the server without importing other clients' ticket or basket rows into the local database. Prefix definitions are shared configuration and can be cached so volunteers can still open their forms offline.

A connection address identifies the server to contact, not a separate event dataset. Pairing with a replacement keeps pending saves. Unpairing pauses their delivery; edits made while that queue is paused stay ordered behind it, and configuring a server resumes delivery.

## Drawing a winner

Entering a positive winning ticket number looks up the buyer through the shared server, including tickets entered on another client. The lookup displays that information without copying the other client's ticket into this client's database. Zero means the basket has not been drawn or its winner was cleared.

The drawing page distinguishes a ticket missing from the server from a lookup limited to this client's local entries. Another workstation may still have unsent entries, so a missing server result is not proof that nobody entered the ticket. Unresolved shared-server lookups retry while the page is open, allowing an entry uploaded later to appear without retyping the winner.

## Rebuilding an empty server

An empty Go server requests recovery data through its authenticated heartbeat response. A client with a valid access key uploads its own saved entries before replaying pending changes. No volunteer has to press Push or pair again if the access key, address and TLS identity still work. A changed address must be configured, a replaced key must be selected or paired, and a changed pinned certificate must be trusted by pairing again.

Recovery requests and acknowledgements survive restarts. Clients are identified separately from their access keys, so two workstations using one key can both contribute, including one which reconnects later. Snapshots fill missing records and missing basket components; they do not overwrite data already accepted for the same component by the replacement. Normal queued edits are then applied in save order. A rejected or interrupted recovery upload leaves the local records and queue intact.

Connected means the server is reachable. During recovery, the client's status bar separately shows that it is restoring its saved data. That client is caught up only after recovery finishes and its pending-save count reaches zero; a connected client cannot certify that a different, offline workstation has sent everything.

Basket descriptions/donors and drawing results are tracked separately. One client can restore a basket description while another restores its winning ticket, in either arrival order. An explicitly cleared winner (`0`) is a saved drawing entry. Older basket rows without component provenance are conservatively treated as owning both components; the upgrade does not guess from empty fields. Normal backup files keep their original format.

The server retains prefix deletions made during recovery so a late snapshot cannot put a deleted prefix back on the menu. As elsewhere in TAM, deleting a prefix keeps its ticket and basket records. Explicitly saving that prefix again recreates it.

If clients contain different edits of the same row, recovery preserves the server's existing value for that row or basket component and each client's local original. It cannot infer which historical edit is newest from legacy records that have no shared revision. Explicit Push or backup restore remains available when an operator chooses which copy to use.

## Compatibility and existing files

Automatic recovery requires the updated Go client and server. The original server API and backup format continue to work, including manual Push. Access keys are never included in a client's event snapshot, and recovery does not bypass authentication.

Updating preserves existing databases. Earlier Go releases could import other clients' rows into them; those historical cached records have no provenance distinguishing them from locally entered records. They are not guessed at or deleted by an upgrade. New remote reads and reconnections do not add those rows to local data.

The browser regression suite lives in `scripts/browser`; backend recovery tests cover authenticated restart, late clients, queued writes, failed uploads and conflicting existing rows. Windows developers should run listening test programs in Linux containers to avoid repeated Firewall prompts.
