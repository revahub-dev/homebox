-- +goose Up
-- Fork addition (use & build): side tables only, no upstream table changes.
create table if not exists x_entity_activities
(
    id             uuid     not null primary key,
    created_at     datetime not null,
    updated_at     datetime not null,
    group_id       uuid     not null
        constraint x_entity_activities_groups references groups on delete cascade,
    entity_id      uuid     not null
        constraint x_entity_activities_entities references entities on delete cascade,
    last_used_at   datetime,
    use_count      integer  default 0 not null,
    snoozed_until  datetime,
    for_sale       bool     default false not null,
    sale_draft     text     default '' not null,
    last_nudged_at datetime
);
create unique index if not exists xentityactivity_entity_id on x_entity_activities (entity_id);
create index if not exists xentityactivity_group_id on x_entity_activities (group_id);

create table if not exists x_idle_digests
(
    id           uuid     not null primary key,
    created_at   datetime not null,
    updated_at   datetime not null,
    group_id     uuid     not null
        constraint x_idle_digests_groups references groups on delete cascade,
    last_sent_at datetime not null,
    item_count   integer  default 0 not null
);
create unique index if not exists xidledigest_group_id on x_idle_digests (group_id);

create table if not exists x_projects
(
    id            uuid     not null primary key,
    created_at    datetime not null,
    updated_at    datetime not null,
    group_id      uuid     not null
        constraint x_projects_groups references groups on delete cascade,
    name          text     not null check (length(name) <= 255),
    description   text     default '' not null,
    build_status  text     default 'missing_parts' not null
        check (build_status in ('buildable_now', 'missing_parts')),
    steps         text     default '[]' not null,
    missing_parts text     default '[]' not null
);
create index if not exists xproject_group_id on x_projects (group_id);

create table if not exists x_project_parts
(
    id         uuid     not null primary key,
    created_at datetime not null,
    updated_at datetime not null,
    entity_id  uuid     not null
        constraint x_project_parts_entities references entities on delete cascade,
    quantity   real     default 1 not null,
    role       text     default '' not null,
    project_id uuid     not null
        constraint x_project_parts_x_projects_parts references x_projects on delete cascade
);
create index if not exists xprojectpart_entity_id on x_project_parts (entity_id);
create unique index if not exists xprojectpart_project_id_entity_id on x_project_parts (project_id, entity_id);

-- +goose Down
drop table if exists x_project_parts;
drop table if exists x_projects;
drop table if exists x_idle_digests;
drop table if exists x_entity_activities;
