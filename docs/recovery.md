# Event data and server recovery

Each client keeps the tickets, basket descriptions and drawing entries saved through that client. The server combines entries from all clients. Reading the combined event does not copy other clients' ticket or basket rows into the local database. Prefix definitions are shared configuration and can be cached for offline forms.

A server address identifies where to connect, not a separate local dataset. Pairing with a replacement keeps this client's entries and pending saves. Unpairing pauses delivery; edits made while that queue is paused stay ordered behind it. Configuring a server resumes delivery.

## Saving and drawing

A client records a durable save request before sending it to the server. The accepted value and its server receipt are retained locally before the request is removed. An interrupted request remains recoverable, including when the server committed but its reply never reached the client. Definitively rejected data stays an error; it is not silently turned into an accepted local entry.

The browser protects edits made while an earlier save or page load is still running. Save generations also reach the client program, so a delayed request from the same page cannot overwrite its later save. Separate volunteers can still deliberately edit the same ticket: normal online saves follow the order accepted by the shared server, as in the original application.

Entering a positive winning ticket number immediately looks up its buyer through the shared server, including a ticket entered on another client. The displayed contact details are not imported into the drawing client's database. Zero means undrawn or explicitly cleared.

Missing tickets, saved tickets with blank contact details, and local-only results during an outage have different messages. A missing server result is not proof that nobody entered the ticket: another client may have unsent entries. While the Drawing page is visible, shared buyer lookups retry every five seconds, including previously found buyers, so a later upload or contact correction appears without retyping the winner. Standalone clients use their own entries without remote retries.

Forms load the server's current rows when opened or reloaded. They do not continuously replace a volunteer's in-progress input with another client's edits. The buyer lookup refresh is separate from loading the drawing's winning number.

## Rebuilding an empty server

An empty updated Go server requests recovery data through its authenticated heartbeat. Every updated client with a valid key contributes its own saved entries before replaying its pending saves. Clients sharing one access key still have separate identities and can contribute independently, including after reconnecting later.

No volunteer needs to press Push when the address, access key and TLS trust still work. A changed address must be configured; a replacement key must be selected or paired; a changed pinned certificate must be trusted by pairing again. Recovery uploads never contain access keys and cannot bypass authentication.

Recovery uses saved per-record history, not arrival time or workstation clocks. When that history proves one correction follows another, the later correction survives either recovery order. A retry of an already accepted old save cannot undo the correction. Basket descriptions/donors and winning tickets have separate ownership and history: restoring one must not clear the other. Empty text and an explicitly cleared winner (`0`) are real saved values.

Components with pending saves travel through the ordered queue after recovery. They are excluded from the recovery snapshot so a correction still waiting for its first server acceptance is not mistaken for a conflicting historical copy. Local backups continue to include those entries and their history.

Prefix deletions retain their history too, so a late older copy does not resurrect a deleted prefix. Deleting a prefix keeps its tickets and baskets, as elsewhere in TAM; explicitly saving the prefix again recreates it.

Connected means reachable. The client separately shows when it is restoring data and how many saves remain queued. That client is caught up only after recovery finishes and the queue is empty. It cannot certify that an offline workstation has contributed.

## Conflicting copies

Some differences cannot be ranked safely: older records without shared history, incomplete correction history, independently edited copies, or cloned data folders that reused an operation number. The server retains those alternatives and requires review instead of silently selecting the first or last arrival. Normal event reads and reports are blocked while conflicts remain, and clients show a Data review warning.

Open **Data review** on the server's password-protected admin page. Compare the alternatives against the paper records, select the correct value and confirm it. Nothing is preselected. If another alternative arrives while the page is open, the choice is rejected until the operator reviews the refreshed list.

The decision keeps the reviewed history. Connected clients that already own the selected value retain its receipt without downloading other clients' data. Previously reviewed alternatives then cannot reopen the same dispute after another server loss; genuinely new conflicting information still requires review. Keep a server backup until the selected value's owning client has synchronized, particularly if that client is offline.

## Backups, Push and restore

New Go backup files retain component ownership, correction history, unresolved alternatives and prefix deletions alongside the original three data lists. A local backup of a descriptions-only client therefore remains descriptions-only after restore. Push Baskets sends only the components that client owns.

The JSON download contains event records and their recovery history. It does not contain settings, access keys or the pending/failed request queues. Preserve the whole client data folder when those must be recovered too.

Push and restore refuse to start while that client has queued saves. Let the queue finish or review refused saves in Settings first; otherwise an older queued change could undo the chosen restore. A deliberate server restore is an operator action and records the restored values as new changes. Restoring a client's own backup preserves its existing history.

Push and restore also wait for that client's recovery upload to finish. **Retry** in Settings deliberately resends the failed saves' values after the existing queue; those values may be older than the current form. The retried values must be retained locally too, so an accepted retry survives another server loss.

The original three-list JSON files still import. They contain no component ownership: a basket in such a file is treated as a complete basket, including its winning ticket. Native files containing the additional metadata require an updated Go server for remote restore; the client will not silently discard that information for an older server. Keep the full native file for future restores even if a legacy application can read its three original lists.

Automatic recovery supplements backups. A destroyed client disk, an offline client, or data entered/restored only on the server cannot be reconstructed from clients that never retained those values. Back up the server and client data folders, and keep the event's paper records.

## Existing databases and compatibility

The updated client still works with the original server API, and legacy backup restore remains supported. Automatic server reconstruction and recovery receipts require the updated Go client and server together.

Existing databases are preserved. Earlier Go releases could cache other clients' rows without recording who entered them, and older basket rows lack component ownership. The upgrade neither guesses their author from blank fields nor deletes them. Such legacy basket rows are conservatively treated as owning both components. New remote reads do not add other clients' ticket or basket rows.

See the [test report](integrity-test-results.md) and [browser test instructions](../scripts/browser/README.md) for verification and reproduction commands. Tests cover specific failures and use cases; they cannot guarantee survival of every hardware failure or establish that an offline client's records have arrived.
