-- +goose Up
-- Fork addition (order import): side tables only, no upstream table changes.
create table if not exists x_inbound_addresses
(
    id               uuid     not null primary key,
    created_at       datetime not null,
    updated_at       datetime not null,
    group_id         uuid     not null
        constraint x_inbound_addresses_groups references groups on delete cascade,
    token            text     not null check (length(token) <= 128),
    allowed_senders  text     default '' not null,
    last_received_at datetime
);
create unique index if not exists xinboundaddress_group_id on x_inbound_addresses (group_id);
create unique index if not exists xinboundaddress_token on x_inbound_addresses (token);

create table if not exists x_inbound_messages
(
    id           uuid     not null primary key,
    created_at   datetime not null,
    updated_at   datetime not null,
    group_id     uuid     not null
        constraint x_inbound_messages_groups references groups on delete cascade,
    channel      text     not null check (channel in ('forward', 'eml', 'photo')),
    status       text     default 'unparsed' not null
        check (status in ('parsed', 'unparsed', 'duplicate', 'failed')),
    message_id   text     default '' not null,
    content_hash text     not null,
    store        text     default '' not null,
    order_number text     default '' not null,
    order_date   datetime,
    order_total  real     default 0 not null,
    sender       text     default '' not null,
    subject      text     default '' not null,
    mime_type    text     default '' not null,
    extractor    text     default '' not null,
    error        text     default '' not null,
    raw          blob
);
create unique index if not exists xinboundmessage_group_id_content_hash on x_inbound_messages (group_id, content_hash);
create index if not exists xinboundmessage_group_id_message_id on x_inbound_messages (group_id, message_id);
create index if not exists xinboundmessage_group_id_store_order_number on x_inbound_messages (group_id, store, order_number);

create table if not exists x_import_candidates
(
    id                 uuid     not null primary key,
    created_at         datetime not null,
    updated_at         datetime not null,
    group_id           uuid     not null
        constraint x_import_candidates_groups references groups on delete cascade,
    kind               text     default 'item' not null check (kind in ('item', 'notice')),
    status             text     default 'pending' not null
        check (status in ('pending', 'accepted', 'merged', 'ignored')),
    name               text     not null,
    quantity           real     default 1 not null,
    unit_price         real     default 0 not null,
    currency           text     default 'JPY' not null,
    store              text     default '' not null,
    order_number       text     default '' not null,
    order_date         datetime,
    asin               text     default '' not null,
    jan                text     default '' not null,
    model_number       text     default '' not null,
    url                text     default '' not null,
    note               text     default '' not null,
    confidence         real     default 0 not null,
    low_confidence     bool     default false not null,
    extractor          text     default '' not null,
    dup_matches        text     default '[]' not null,
    entity_id          uuid
        constraint x_import_candidates_entities references entities on delete set null,
    inbound_message_id uuid     not null
        constraint x_import_candidates_x_inbound_messages_candidates references x_inbound_messages on delete cascade
);
create index if not exists ximportcandidate_group_id_status on x_import_candidates (group_id, status);
