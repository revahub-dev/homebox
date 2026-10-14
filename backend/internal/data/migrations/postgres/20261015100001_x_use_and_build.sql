-- +goose Up
-- Fork addition (use & build): side tables only, no upstream table changes.
CREATE TABLE IF NOT EXISTS "x_entity_activities" (
    "id" uuid NOT NULL,
    "created_at" timestamptz NOT NULL,
    "updated_at" timestamptz NOT NULL,
    "group_id" uuid NOT NULL,
    "entity_id" uuid NOT NULL,
    "last_used_at" timestamptz NULL,
    "use_count" bigint NOT NULL DEFAULT 0,
    "snoozed_until" timestamptz NULL,
    "for_sale" boolean NOT NULL DEFAULT false,
    "sale_draft" text NOT NULL DEFAULT '',
    "last_nudged_at" timestamptz NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "x_entity_activities_groups" FOREIGN KEY ("group_id") REFERENCES "groups" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "x_entity_activities_entities" FOREIGN KEY ("entity_id") REFERENCES "entities" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS "xentityactivity_entity_id" ON "x_entity_activities" ("entity_id");
CREATE INDEX IF NOT EXISTS "xentityactivity_group_id" ON "x_entity_activities" ("group_id");

CREATE TABLE IF NOT EXISTS "x_idle_digests" (
    "id" uuid NOT NULL,
    "created_at" timestamptz NOT NULL,
    "updated_at" timestamptz NOT NULL,
    "group_id" uuid NOT NULL,
    "last_sent_at" timestamptz NOT NULL,
    "item_count" bigint NOT NULL DEFAULT 0,
    PRIMARY KEY ("id"),
    CONSTRAINT "x_idle_digests_groups" FOREIGN KEY ("group_id") REFERENCES "groups" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS "xidledigest_group_id" ON "x_idle_digests" ("group_id");

CREATE TABLE IF NOT EXISTS "x_projects" (
    "id" uuid NOT NULL,
    "created_at" timestamptz NOT NULL,
    "updated_at" timestamptz NOT NULL,
    "group_id" uuid NOT NULL,
    "name" character varying(255) NOT NULL,
    "description" text NOT NULL DEFAULT '',
    "build_status" character varying NOT NULL DEFAULT 'missing_parts'
        CHECK ("build_status" IN ('buildable_now', 'missing_parts')),
    "steps" text NOT NULL DEFAULT '[]',
    "missing_parts" text NOT NULL DEFAULT '[]',
    PRIMARY KEY ("id"),
    CONSTRAINT "x_projects_groups" FOREIGN KEY ("group_id") REFERENCES "groups" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS "xproject_group_id" ON "x_projects" ("group_id");

CREATE TABLE IF NOT EXISTS "x_project_parts" (
    "id" uuid NOT NULL,
    "created_at" timestamptz NOT NULL,
    "updated_at" timestamptz NOT NULL,
    "entity_id" uuid NOT NULL,
    "quantity" double precision NOT NULL DEFAULT 1,
    "role" character varying(255) NOT NULL DEFAULT '',
    "project_id" uuid NOT NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "x_project_parts_entities" FOREIGN KEY ("entity_id") REFERENCES "entities" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "x_project_parts_x_projects_parts" FOREIGN KEY ("project_id") REFERENCES "x_projects" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS "xprojectpart_entity_id" ON "x_project_parts" ("entity_id");
CREATE UNIQUE INDEX IF NOT EXISTS "xprojectpart_project_id_entity_id" ON "x_project_parts" ("project_id", "entity_id");

-- +goose Down
DROP TABLE IF EXISTS "x_project_parts";
DROP TABLE IF EXISTS "x_projects";
DROP TABLE IF EXISTS "x_idle_digests";
DROP TABLE IF EXISTS "x_entity_activities";
