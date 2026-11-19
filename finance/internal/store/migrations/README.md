# Migrations

Numbered SQL files, applied in order at startup by `store.Migrate` and recorded in
`schema_migrations`. Each file runs in its own transaction.

- Name them `NNNN_short_description.sql` (e.g. `0002_add_notes.sql`).
- Never edit or renumber a file once it has been deployed: add a new one instead.
- Non-`.sql` files (like this one) are ignored.
