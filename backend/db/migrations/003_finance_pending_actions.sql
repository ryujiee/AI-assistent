-- 003 finance pending actions: what the bot is waiting for.
--
-- When a WhatsApp entry cannot be confirmed yet, the draft is the PENDING
-- transaction itself (audited, visible in the panel). This table records the
-- conversation state around it: which question was asked, which answer is
-- expected, which message asked it, and until when a reply still counts.
-- A reply like "sim" or "mercado" is resolved against this row by the
-- backend, never reconstructed by the model.

CREATE TABLE finance_pending_actions (
    id BIGSERIAL PRIMARY KEY,
    workspace_id BIGINT NOT NULL REFERENCES finance_workspaces(id) ON DELETE CASCADE,
    member_id BIGINT REFERENCES finance_members(id),
    chat_jid TEXT NOT NULL,
    source_inbox_id BIGINT REFERENCES finance_inbox(id),
    transaction_id BIGINT NOT NULL REFERENCES finance_transactions(id),
    intent TEXT NOT NULL DEFAULT 'CREATE_TRANSACTION' CHECK (intent IN ('CREATE_TRANSACTION')),
    awaiting TEXT NOT NULL CHECK (awaiting IN ('CATEGORY', 'DATE', 'CONFIRM', 'CONFIRM_DUPLICATE')),
    suggested_category_id BIGINT REFERENCES finance_categories(id),
    status TEXT NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN', 'EXECUTED', 'CANCELLED', 'EXPIRED', 'SUPERSEDED')),
    -- The bot message that asked the question (a quoted reply points here).
    question_message_id TEXT,
    -- Unresolved replies so far; the conversation gives up after a few.
    attempts INT NOT NULL DEFAULT 0,
    expires_at TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- One open question per member keeps "sim" unambiguous.
CREATE UNIQUE INDEX finance_pending_open_uq ON finance_pending_actions (workspace_id, member_id) WHERE status = 'OPEN';
CREATE INDEX finance_pending_question_idx ON finance_pending_actions (workspace_id, question_message_id) WHERE question_message_id IS NOT NULL;
CREATE INDEX finance_pending_tx_idx ON finance_pending_actions (workspace_id, transaction_id);
