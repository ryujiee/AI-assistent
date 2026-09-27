-- 002 finance: ledger of the WhatsApp finance module.
--
-- Money is BIGINT cents, never float. Instants are TIMESTAMPTZ.
-- transaction_date is the civil day in the application time zone
-- (America/Sao_Paulo), so reports never depend on the server time zone.
-- Every finance row belongs to a workspace; queries always filter by it.

CREATE TABLE finance_workspaces (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    group_jid TEXT UNIQUE,
    group_name TEXT,
    group_linked_at TIMESTAMPTZ,
    confidence_threshold NUMERIC(3,2) NOT NULL DEFAULT 0.75
        CHECK (confidence_threshold BETWEEN 0 AND 1),
    monthly_summary TEXT NOT NULL DEFAULT 'off'
        CHECK (monthly_summary IN ('off', 'last_day', 'first_day')),
    weekly_summary BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE finance_members (
    id BIGSERIAL PRIMARY KEY,
    workspace_id BIGINT NOT NULL REFERENCES finance_workspaces(id) ON DELETE CASCADE,
    -- Primary WhatsApp identity: phone-number JID when known, LID otherwise.
    jid TEXT NOT NULL,
    lid TEXT,
    phone_number TEXT,
    display_name TEXT NOT NULL CHECK (btrim(display_name) <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, jid)
);
CREATE UNIQUE INDEX finance_members_lid_uq ON finance_members (workspace_id, lid) WHERE lid IS NOT NULL;

CREATE TABLE finance_categories (
    id BIGSERIAL PRIMARY KEY,
    workspace_id BIGINT NOT NULL REFERENCES finance_workspaces(id) ON DELETE CASCADE,
    -- One level of subcategories; enforced by the service.
    parent_id BIGINT REFERENCES finance_categories(id),
    name TEXT NOT NULL CHECK (btrim(name) <> ''),
    icon TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL DEFAULT 'EXPENSE' CHECK (kind IN ('EXPENSE', 'INCOME')),
    essentiality TEXT NOT NULL DEFAULT 'IMPORTANT'
        CHECK (essentiality IN ('ESSENTIAL', 'IMPORTANT', 'DISCRETIONARY')),
    monthly_budget_cents BIGINT CHECK (monthly_budget_cents IS NULL OR monthly_budget_cents > 0),
    sort_order INT NOT NULL DEFAULT 0,
    archived_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX finance_categories_name_uq
    ON finance_categories (workspace_id, (COALESCE(parent_id, 0)), (lower(name)));

-- Receipts. Kept private in the database and only served through an
-- authenticated endpoint; the same file is stored once per workspace.
CREATE TABLE finance_attachments (
    id BIGSERIAL PRIMARY KEY,
    workspace_id BIGINT NOT NULL REFERENCES finance_workspaces(id) ON DELETE CASCADE,
    sha256 TEXT NOT NULL,
    mime TEXT NOT NULL CHECK (mime IN ('image/jpeg', 'image/png', 'image/webp', 'application/pdf')),
    size_bytes INT NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 10485760),
    data BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, sha256)
);

-- Every accepted WhatsApp message. The unique key makes redelivered events a
-- no-op; the status machine lets a crashed run be resumed.
CREATE TABLE finance_inbox (
    id BIGSERIAL PRIMARY KEY,
    workspace_id BIGINT NOT NULL REFERENCES finance_workspaces(id) ON DELETE CASCADE,
    chat_jid TEXT NOT NULL,
    sender_jid TEXT NOT NULL,
    wa_message_id TEXT NOT NULL,
    member_id BIGINT REFERENCES finance_members(id),
    kind TEXT NOT NULL CHECK (kind IN ('TEXT', 'AUDIO', 'IMAGE', 'DOCUMENT')),
    text TEXT,
    quoted_message_id TEXT,
    -- Serialized WhatsApp media message so a retry can download it again.
    -- Cleared once the message is DONE or FAILED.
    media_ref BYTEA,
    media_mime TEXT,
    media_size BIGINT,
    attachment_id BIGINT REFERENCES finance_attachments(id),
    message_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'PROCESSING', 'DONE', 'FAILED')),
    attempts INT NOT NULL DEFAULT 0,
    processing_at TIMESTAMPTZ,
    next_attempt_at TIMESTAMPTZ,
    last_error_code TEXT,
    reply_message_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chat_jid, sender_jid, wa_message_id)
);
CREATE INDEX finance_inbox_queue_idx ON finance_inbox (workspace_id, status, id);
CREATE INDEX finance_inbox_reply_idx ON finance_inbox (workspace_id, reply_message_id) WHERE reply_message_id IS NOT NULL;
CREATE INDEX finance_inbox_message_idx ON finance_inbox (workspace_id, wa_message_id);

CREATE TABLE finance_transactions (
    id BIGSERIAL PRIMARY KEY,
    workspace_id BIGINT NOT NULL REFERENCES finance_workspaces(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('EXPENSE', 'INCOME', 'TRANSFER', 'REFUND')),
    -- PLANNED (bills to pay) can be added later without touching existing rows.
    status TEXT NOT NULL DEFAULT 'CONFIRMED' CHECK (status IN ('CONFIRMED', 'PENDING')),
    amount_cents BIGINT NOT NULL CHECK (amount_cents > 0 AND amount_cents <= 100000000000),
    currency CHAR(3) NOT NULL DEFAULT 'BRL' CHECK (currency ~ '^[A-Z]{3}$'),
    description TEXT NOT NULL DEFAULT '',
    merchant TEXT,
    category_id BIGINT REFERENCES finance_categories(id),
    transaction_date DATE NOT NULL,
    payer_member_id BIGINT REFERENCES finance_members(id),
    created_by_member_id BIGINT REFERENCES finance_members(id),
    shared BOOLEAN NOT NULL DEFAULT true,
    payment_method TEXT CHECK (payment_method IS NULL OR payment_method IN
        ('PIX', 'CREDIT_CARD', 'DEBIT_CARD', 'CASH', 'BOLETO', 'TRANSFER', 'OTHER')),
    -- PIX end-to-end id when a receipt shows one; used for duplicate detection.
    external_ref TEXT,
    source TEXT NOT NULL CHECK (source IN ('WHATSAPP_TEXT', 'WHATSAPP_AUDIO', 'WHATSAPP_RECEIPT', 'WEB')),
    source_inbox_id BIGINT REFERENCES finance_inbox(id),
    -- Position of the item when one message lists several expenses.
    source_item SMALLINT NOT NULL DEFAULT 0 CHECK (source_item >= 0),
    attachment_id BIGINT REFERENCES finance_attachments(id),
    reply_message_id TEXT,
    ai_confidence NUMERIC(3,2) CHECK (ai_confidence IS NULL OR ai_confidence BETWEEN 0 AND 1),
    pending_reason TEXT,
    possible_duplicate_of BIGINT REFERENCES finance_transactions(id),
    notes TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    CHECK (type = 'TRANSFER' OR category_id IS NOT NULL OR status = 'PENDING')
);
-- The same WhatsApp message can never create the same transaction twice.
CREATE UNIQUE INDEX finance_transactions_source_uq
    ON finance_transactions (source_inbox_id, source_item) WHERE source_inbox_id IS NOT NULL;
CREATE INDEX finance_transactions_date_idx ON finance_transactions (workspace_id, transaction_date) WHERE deleted_at IS NULL;
CREATE INDEX finance_transactions_category_idx ON finance_transactions (workspace_id, category_id);
CREATE INDEX finance_transactions_extref_idx ON finance_transactions (workspace_id, external_ref) WHERE external_ref IS NOT NULL;
CREATE INDEX finance_transactions_attachment_idx ON finance_transactions (workspace_id, attachment_id) WHERE attachment_id IS NOT NULL;

-- Audit trail. Written in the same SQL transaction as the change it records.
CREATE TABLE finance_transaction_events (
    id BIGSERIAL PRIMARY KEY,
    workspace_id BIGINT NOT NULL REFERENCES finance_workspaces(id) ON DELETE CASCADE,
    transaction_id BIGINT NOT NULL REFERENCES finance_transactions(id),
    action TEXT NOT NULL CHECK (action IN ('CREATE', 'UPDATE', 'DELETE', 'UNDO')),
    before JSONB,
    after JSONB,
    actor_member_id BIGINT REFERENCES finance_members(id),
    channel TEXT NOT NULL CHECK (channel IN ('WHATSAPP', 'WEB', 'SYSTEM')),
    source_inbox_id BIGINT REFERENCES finance_inbox(id),
    undone_by_event_id BIGINT REFERENCES finance_transaction_events(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX finance_events_tx_idx ON finance_transaction_events (workspace_id, transaction_id, id);
CREATE INDEX finance_events_inbox_idx ON finance_transaction_events (source_inbox_id) WHERE source_inbox_id IS NOT NULL;
CREATE INDEX finance_events_actor_idx ON finance_transaction_events (workspace_id, actor_member_id, id DESC);

-- Deduplication and cooldown of alerts and scheduled summaries.
CREATE TABLE finance_alerts_sent (
    workspace_id BIGINT NOT NULL REFERENCES finance_workspaces(id) ON DELETE CASCADE,
    alert_key TEXT NOT NULL,
    sent_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, alert_key)
);
