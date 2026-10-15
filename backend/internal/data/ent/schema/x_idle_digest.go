package schema

// Fork addition (use & build: idle-item nudges). See FORK_NOTES.md.

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"

	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/schema/mixins"
)

// XIdleDigest remembers when a collection last got its idle-item digest, so
// the weekly cadence survives restarts.
type XIdleDigest struct {
	ent.Schema
}

func (XIdleDigest) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "x_idle_digests"}}
}

func (XIdleDigest) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.BaseMixin{}}
}

func (XIdleDigest) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("group_id", uuid.UUID{}),
		field.Time("last_sent_at"),
		field.Int("item_count").Default(0),
	}
}

func (XIdleDigest) Indexes() []ent.Index {
	return []ent.Index{index.Fields("group_id").Unique()}
}
