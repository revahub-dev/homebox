package schema

// Fork addition (order import). See FORK_NOTES.md.

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"

	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/schema/mixins"
)

// XImportCandidate is one line item in the review inbox.
type XImportCandidate struct {
	ent.Schema
}

func (XImportCandidate) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "x_import_candidates"}}
}

func (XImportCandidate) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.BaseMixin{}}
}

func (XImportCandidate) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("group_id", uuid.UUID{}),
		field.UUID("inbound_message_id", uuid.UUID{}),
		// kind "item" is a purchasable line item; "notice" is a message the
		// user must look at (e.g. a mail-provider forwarding confirmation).
		field.Enum("kind").Values("item", "notice").Default("item"),
		field.Enum("status").Values("pending", "accepted", "merged", "ignored").Default("pending"),
		field.String("name").MaxLen(500),
		field.Float("quantity").Default(1),
		field.Float("unit_price").Default(0),
		field.String("currency").Default("JPY").MaxLen(8),
		field.String("store").Default("").MaxLen(32),
		field.String("order_number").Default("").MaxLen(128),
		field.Time("order_date").Optional().Nillable(),
		field.String("asin").Default("").MaxLen(64),
		field.String("jan").Default("").MaxLen(64),
		field.String("model_number").Default("").MaxLen(255),
		field.String("url").Default("").MaxLen(2048),
		field.Text("note").Default(""),
		// 0..1. Parser output is high, LLM text fallback medium, photos low.
		field.Float("confidence").Default(0),
		field.Bool("low_confidence").Default(false),
		field.String("extractor").Default("").MaxLen(64),
		// JSON snapshot of dupcheck matches at import time.
		field.Text("dup_matches").Default("[]"),
		// The entity created (accept) or merged into. FK only in migrations.
		field.UUID("entity_id", uuid.UUID{}).Optional().Nillable(),
	}
}

func (XImportCandidate) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("message", XInboundMessage.Type).
			Ref("candidates").
			Field("inbound_message_id").
			Unique().
			Required(),
	}
}

func (XImportCandidate) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("group_id", "status"),
	}
}
