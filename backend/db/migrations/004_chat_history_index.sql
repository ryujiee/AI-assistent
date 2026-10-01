-- 004 chat history index.
--
-- Every secretary turn reads the last messages of one chat
-- (WHERE jid = $1 ORDER BY timestamp DESC LIMIT n). Without this index the
-- whole table is sorted on each message, and the table only grows.

CREATE INDEX IF NOT EXISTS chat_history_jid_timestamp_idx ON chat_history (jid, timestamp DESC);
