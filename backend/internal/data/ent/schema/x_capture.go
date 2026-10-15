package schema

// Fork addition (photo-first capture). See FORK_NOTES.md.
// group_id, user_id, location_id and entity_id are plain columns with FKs only
// in the SQL migrations, so upstream schemas are untouched.

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"

	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/schema/mixins"
)

// XCaptureSession is one Snap run: a batch of photos taken in one mode.
type XCaptureSession struct{ ent.Schema }

func (XCaptureSession) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "x_capture_sessions"}}
}

func (XCaptureSession) Mixin() []ent.Mixin { return []ent.Mixin{mixins.BaseMixin{}} }

func (XCaptureSession) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("group_id", uuid.UUID{}),
		field.UUID("user_id", uuid.UUID{}),
		field.Enum("mode").Values("item", "shelf"),
		field.UUID("location_id", uuid.UUID{}).Optional().Nillable(),
		field.Enum("status").Values("open", "done").Default("open"),
	}
}

func (XCaptureSession) Indexes() []ent.Index {
	return []ent.Index{index.Fields("group_id", "created_at")}
}

// XCapturePhoto is one uploaded photo and its place in the analysis queue.
type XCapturePhoto struct{ ent.Schema }

func (XCapturePhoto) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "x_capture_photos"}}
}

func (XCapturePhoto) Mixin() []ent.Mixin { return []ent.Mixin{mixins.BaseMixin{}} }

func (XCapturePhoto) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("group_id", uuid.UUID{}),
		field.UUID("session_id", uuid.UUID{}),
		field.Enum("status").Values("queued", "running", "done", "failed").Default("queued"),
		field.Int("attempts").Default(0),
		field.String("error").Default("").MaxLen(1000),
		field.String("mime_type").Default("image/jpeg").MaxLen(64),
		field.Int("width").Default(0),
		field.Int("height").Default(0),
		// content_hash dedupes an identical photo within a session.
		field.String("content_hash").MaxLen(64),
		// image is the normalised JPEG sent to the model; purged after retention.
		field.Bytes("image").Optional(),
		field.String("provider").Default("").MaxLen(64),
		field.Int("latency_ms").Default(0),
		field.Int("detections").Default(0),
		field.Time("locked_at").Optional().Nillable(),
	}
}

func (XCapturePhoto) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "created_at"),
		index.Fields("session_id", "content_hash").Unique(),
		index.Fields("group_id", "created_at"),
	}
}

// XCaptureItem is one detection awaiting review.
type XCaptureItem struct{ ent.Schema }

func (XCaptureItem) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "x_capture_items"}}
}

func (XCaptureItem) Mixin() []ent.Mixin { return []ent.Mixin{mixins.BaseMixin{}} }

func (XCaptureItem) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("group_id", uuid.UUID{}),
		field.UUID("session_id", uuid.UUID{}),
		field.UUID("photo_id", uuid.UUID{}),
		field.Enum("state").Values("pending", "accepted", "merged", "ignored").Default("pending"),
		field.String("name").NotEmpty().MaxLen(255),
		field.String("brand").Default("").MaxLen(255),
		field.String("model_number").Default("").MaxLen(255),
		field.String("category").Default("").MaxLen(64),
		field.Float("quantity").Default(1),
		field.Float("confidence").Default(0),
		field.Bool("low_confidence").Default(false),
		// bbox is JSON {"x","y","w","h"} normalised 0..1, or empty.
		field.String("bbox").Default("").MaxLen(200),
		field.Bytes("crop").Optional(),
		field.Bool("has_crop").Default(false),
		field.Text("dup_matches").Default("[]"),
		field.UUID("target_location_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("entity_id", uuid.UUID{}).Optional().Nillable(),
		// attachment_id and applied_quantity let Undo reverse accept/merge.
		field.UUID("attachment_id", uuid.UUID{}).Optional().Nillable(),
		field.Float("applied_quantity").Default(0),
		field.Time("handled_at").Optional().Nillable(),
	}
}

func (XCaptureItem) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("session_id", "state"),
		index.Fields("photo_id"),
		index.Fields("group_id", "created_at"),
	}
}
