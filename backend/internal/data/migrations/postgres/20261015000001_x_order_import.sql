-- +goose Up
-- Fork addition (order import): side tables only, no upstream table changes.
CREATE TABLE IF NOT EXISTS "x_inbound_addresses" (
    "id" uuid NOT NULL,
    "created_at" timestamptz NOT NULL,
    "updated_at" timestamptz NOT NULL,
    "group_id" uuid NOT NULL,
    "token" character varying(128) NOT NULL,
    "allowed_senders" text NOT NULL DEFAULT '',
    "last_received_at" timestamptz NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "x_inbound_addresses_groups" FOREIGN KEY ("group_id") REFERENCES "groups" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS "xinboundaddress_group_id" ON "x_inbound_addresses" ("group_id");
CREATE UNIQUE INDEX IF NOT EXISTS "xinboundaddress_token" ON "x_inbound_addresses" ("token");

CREATE TABLE IF NOT EXISTS "x_inbound_messages" (
    "id" uuid NOT NULL,
    "created_at" timestamptz NOT NULL,
    "updated_at" timestamptz NOT NULL,
    "group_id" uuid NOT NULL,
    "channel" character varying NOT NULL CHECK ("channel" IN ('forward', 'eml', 'photo')),
    "status" character varying NOT NULL DEFAULT 'unparsed'
        CHECK ("status" IN ('parsed', 'unparsed', 'duplicate', 'failed')),
    "message_id" character varying(998) NOT NULL DEFAULT '',
    "content_hash" character varying(64) NOT NULL,
    "store" character varying(32) NOT NULL DEFAULT '',
    "order_number" character varying(128) NOT NULL DEFAULT '',
    "order_date" timestamptz NULL,
    "order_total" double precision NOT NULL DEFAULT 0,
    "sender" character varying(320) NOT NULL DEFAULT '',
    "subject" character varying(998) NOT NULL DEFAULT '',
    "mime_type" character varying(128) NOT NULL DEFAULT '',
    "extractor" character varying(64) NOT NULL DEFAULT '',
    "error" character varying(1000) NOT NULL DEFAULT '',
    "raw" bytea NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "x_inbound_messages_groups" FOREIGN KEY ("group_id") REFERENCES "groups" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS "xinboundmessage_group_id_content_hash" ON "x_inbound_messages" ("group_id", "content_hash");
CREATE INDEX IF NOT EXISTS "xinboundmessage_group_id_message_id" ON "x_inbound_messages" ("group_id", "message_id");
CREATE INDEX IF NOT EXISTS "xinboundmessage_group_id_store_order_number" ON "x_inbound_messages" ("group_id", "store", "order_number");

CREATE TABLE IF NOT EXISTS "x_import_candidates" (
    "id" uuid NOT NULL,
    "created_at" timestamptz NOT NULL,
    "updated_at" timestamptz NOT NULL,
    "group_id" uuid NOT NULL,
    "kind" character varying NOT NULL DEFAULT 'item' CHECK ("kind" IN ('item', 'notice')),
    "status" character varying NOT NULL DEFAULT 'pending'
        CHECK ("status" IN ('pending', 'accepted', 'merged', 'ignored')),
    "name" character varying(500) NOT NULL,
    "quantity" double precision NOT NULL DEFAULT 1,
    "unit_price" double precision NOT NULL DEFAULT 0,
    "currency" character varying(8) NOT NULL DEFAULT 'JPY',
    "store" character varying(32) NOT NULL DEFAULT '',
    "order_number" character varying(128) NOT NULL DEFAULT '',
    "order_date" timestamptz NULL,
    "asin" character varying(64) NOT NULL DEFAULT '',
    "jan" character varying(64) NOT NULL DEFAULT '',
    "model_number" character varying(255) NOT NULL DEFAULT '',
    "url" character varying(2048) NOT NULL DEFAULT '',
    "note" text NOT NULL DEFAULT '',
    "confidence" double precision NOT NULL DEFAULT 0,
    "low_confidence" boolean NOT NULL DEFAULT false,
    "extractor" character varying(64) NOT NULL DEFAULT '',
    "dup_matches" text NOT NULL DEFAULT '[]',
    "entity_id" uuid NULL,
    "inbound_message_id" uuid NOT NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "x_import_candidates_groups" FOREIGN KEY ("group_id") REFERENCES "groups" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "x_import_candidates_entities" FOREIGN KEY ("entity_id") REFERENCES "entities" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
    CONSTRAINT "x_import_candidates_x_inbound_messages_candidates" FOREIGN KEY ("inbound_message_id") REFERENCES "x_inbound_messages" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS "ximportcandidate_group_id_status" ON "x_import_candidates" ("group_id", "status");
