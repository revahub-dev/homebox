-- +goose Up
-- Fork addition (photo-first capture): side tables only, no upstream table changes.
CREATE TABLE IF NOT EXISTS "x_capture_sessions" (
    "id" uuid NOT NULL,
    "created_at" timestamptz NOT NULL,
    "updated_at" timestamptz NOT NULL,
    "group_id" uuid NOT NULL,
    "user_id" uuid NOT NULL,
    "mode" character varying NOT NULL CHECK ("mode" IN ('item', 'shelf')),
    "location_id" uuid NULL,
    "status" character varying NOT NULL DEFAULT 'open' CHECK ("status" IN ('open', 'done')),
    PRIMARY KEY ("id"),
    CONSTRAINT "x_capture_sessions_groups" FOREIGN KEY ("group_id") REFERENCES "groups" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "x_capture_sessions_users" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "x_capture_sessions_entities" FOREIGN KEY ("location_id") REFERENCES "entities" ("id") ON UPDATE NO ACTION ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS "xcapturesession_group_id_created_at" ON "x_capture_sessions" ("group_id", "created_at");

CREATE TABLE IF NOT EXISTS "x_capture_photos" (
    "id" uuid NOT NULL,
    "created_at" timestamptz NOT NULL,
    "updated_at" timestamptz NOT NULL,
    "group_id" uuid NOT NULL,
    "session_id" uuid NOT NULL,
    "status" character varying NOT NULL DEFAULT 'queued' CHECK ("status" IN ('queued', 'running', 'done', 'failed')),
    "attempts" bigint NOT NULL DEFAULT 0,
    "error" character varying(1000) NOT NULL DEFAULT '',
    "mime_type" character varying(64) NOT NULL DEFAULT 'image/jpeg',
    "width" bigint NOT NULL DEFAULT 0,
    "height" bigint NOT NULL DEFAULT 0,
    "content_hash" character varying(64) NOT NULL,
    "image" bytea NULL,
    "provider" character varying(64) NOT NULL DEFAULT '',
    "latency_ms" bigint NOT NULL DEFAULT 0,
    "detections" bigint NOT NULL DEFAULT 0,
    "locked_at" timestamptz NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "x_capture_photos_groups" FOREIGN KEY ("group_id") REFERENCES "groups" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "x_capture_photos_x_capture_sessions" FOREIGN KEY ("session_id") REFERENCES "x_capture_sessions" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS "xcapturephoto_status_created_at" ON "x_capture_photos" ("status", "created_at");
CREATE UNIQUE INDEX IF NOT EXISTS "xcapturephoto_session_id_content_hash" ON "x_capture_photos" ("session_id", "content_hash");
CREATE INDEX IF NOT EXISTS "xcapturephoto_group_id_created_at" ON "x_capture_photos" ("group_id", "created_at");

CREATE TABLE IF NOT EXISTS "x_capture_items" (
    "id" uuid NOT NULL,
    "created_at" timestamptz NOT NULL,
    "updated_at" timestamptz NOT NULL,
    "group_id" uuid NOT NULL,
    "session_id" uuid NOT NULL,
    "photo_id" uuid NOT NULL,
    "state" character varying NOT NULL DEFAULT 'pending' CHECK ("state" IN ('pending', 'accepted', 'merged', 'ignored')),
    "name" character varying(255) NOT NULL,
    "brand" character varying(255) NOT NULL DEFAULT '',
    "model_number" character varying(255) NOT NULL DEFAULT '',
    "category" character varying(64) NOT NULL DEFAULT '',
    "quantity" double precision NOT NULL DEFAULT 1,
    "confidence" double precision NOT NULL DEFAULT 0,
    "low_confidence" boolean NOT NULL DEFAULT false,
    "bbox" character varying(200) NOT NULL DEFAULT '',
    "crop" bytea NULL,
    "has_crop" boolean NOT NULL DEFAULT false,
    "dup_matches" text NOT NULL DEFAULT '[]',
    "target_location_id" uuid NULL,
    "entity_id" uuid NULL,
    "attachment_id" uuid NULL,
    "applied_quantity" double precision NOT NULL DEFAULT 0,
    "handled_at" timestamptz NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "x_capture_items_groups" FOREIGN KEY ("group_id") REFERENCES "groups" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "x_capture_items_x_capture_sessions" FOREIGN KEY ("session_id") REFERENCES "x_capture_sessions" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "x_capture_items_x_capture_photos" FOREIGN KEY ("photo_id") REFERENCES "x_capture_photos" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "x_capture_items_locations" FOREIGN KEY ("target_location_id") REFERENCES "entities" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
    CONSTRAINT "x_capture_items_entities" FOREIGN KEY ("entity_id") REFERENCES "entities" ("id") ON UPDATE NO ACTION ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS "xcaptureitem_session_id_state" ON "x_capture_items" ("session_id", "state");
CREATE INDEX IF NOT EXISTS "xcaptureitem_photo_id" ON "x_capture_items" ("photo_id");
CREATE INDEX IF NOT EXISTS "xcaptureitem_group_id_created_at" ON "x_capture_items" ("group_id", "created_at");

-- +goose Down
DROP TABLE IF EXISTS "x_capture_items";
DROP TABLE IF EXISTS "x_capture_photos";
DROP TABLE IF EXISTS "x_capture_sessions";
