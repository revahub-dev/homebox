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

// XEntityActivity is a side table keyed by entity ID that records when an
// item was last used and the state of its idle nudge. group_id/entity_id are
// plain columns with FKs only in the SQL migrations, so upstream schemas are
// untouched. Edits are tracked through the entity's own updated_at.
type XEntityActivity struct {
	ent.Schema
}

func (XEntityActivity) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "x_entity_activities"}}
}

func (XEntityActivity) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.BaseMixin{}}
}

func (XEntityActivity) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("group_id", uuid.UUID{}),
		field.UUID("entity_id", uuid.UUID{}),
		field.Time("last_used_at").Optional().Nillable(),
		field.Int("use_count").Default(0),
		field.Time("snoozed_until").Optional().Nillable(),
		field.Bool("for_sale").Default(false),
		field.Text("sale_draft").Default(""),
		field.Time("last_nudged_at").Optional().Nillable(),
	}
}

func (XEntityActivity) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("entity_id").Unique(),
		index.Fields("group_id"),
	}
}
