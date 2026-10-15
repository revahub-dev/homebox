-- +goose Up
-- Fork addition (photo-first capture): side tables only, no upstream table changes.
create table if not exists x_capture_sessions
(
    id          uuid     not null primary key,
    created_at  datetime not null,
    updated_at  datetime not null,
    group_id    uuid     not null
        constraint x_capture_sessions_groups references groups on delete cascade,
    user_id     uuid     not null
        constraint x_capture_sessions_users references users on delete cascade,
    mode        text     not null check (mode in ('item', 'shelf')),
    location_id uuid
        constraint x_capture_sessions_entities references entities on delete set null,
    status      text     default 'open' not null check (status in ('open', 'done'))
);
create index if not exists xcapturesession_group_id_created_at on x_capture_sessions (group_id, created_at);

create table if not exists x_capture_photos
(
    id           uuid     not null primary key,
    created_at   datetime not null,
    updated_at   datetime not null,
    group_id     uuid     not null
        constraint x_capture_photos_groups references groups on delete cascade,
    session_id   uuid     not null
        constraint x_capture_photos_x_capture_sessions references x_capture_sessions on delete cascade,
    status       text     default 'queued' not null check (status in ('queued', 'running', 'done', 'failed')),
    attempts     integer  default 0 not null,
    error        text     default '' not null,
    mime_type    text     default 'image/jpeg' not null,
    width        integer  default 0 not null,
    height       integer  default 0 not null,
    content_hash text     not null,
    image        blob,
    provider     text     default '' not null,
    latency_ms   integer  default 0 not null,
    detections   integer  default 0 not null,
    locked_at    datetime
);
create index if not exists xcapturephoto_status_created_at on x_capture_photos (status, created_at);
create unique index if not exists xcapturephoto_session_id_content_hash on x_capture_photos (session_id, content_hash);
create index if not exists xcapturephoto_group_id_created_at on x_capture_photos (group_id, created_at);

create table if not exists x_capture_items
(
    id                 uuid     not null primary key,
    created_at         datetime not null,
    updated_at         datetime not null,
    group_id           uuid     not null
        constraint x_capture_items_groups references groups on delete cascade,
    session_id         uuid     not null
        constraint x_capture_items_x_capture_sessions references x_capture_sessions on delete cascade,
    photo_id           uuid     not null
        constraint x_capture_items_x_capture_photos references x_capture_photos on delete cascade,
    state              text     default 'pending' not null check (state in ('pending', 'accepted', 'merged', 'ignored')),
    name               text     not null,
    brand              text     default '' not null,
    model_number       text     default '' not null,
    category           text     default '' not null,
    quantity           real     default 1 not null,
    confidence         real     default 0 not null,
    low_confidence     bool     default false not null,
    bbox               text     default '' not null,
    crop               blob,
    has_crop           bool     default false not null,
    dup_matches        text     default '[]' not null,
    target_location_id uuid
        constraint x_capture_items_locations references entities on delete set null,
    entity_id          uuid
        constraint x_capture_items_entities references entities on delete set null,
    attachment_id      uuid,
    applied_quantity   real     default 0 not null,
    handled_at         datetime
);
create index if not exists xcaptureitem_session_id_state on x_capture_items (session_id, state);
create index if not exists xcaptureitem_photo_id on x_capture_items (photo_id);
create index if not exists xcaptureitem_group_id_created_at on x_capture_items (group_id, created_at);

-- +goose Down
drop table if exists x_capture_items;
drop table if exists x_capture_photos;
drop table if exists x_capture_sessions;
