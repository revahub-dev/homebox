package usebuild

// Fork addition. See FORK_NOTES.md.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/aichat"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
)

// Notify sends one message to one notifier URL. In production it is
// validate.ValidateNotifierURL + validate.SendNotifierMessage.
type Notify func(url, msg string) error

// IdleSettings resolves IdleConf defaults.
func IdleSettings(c config.IdleConf) config.IdleConf {
	if c.Days <= 0 {
		c.Days = 180
	}
	if c.DigestLimit <= 0 {
		c.DigestLimit = 10
	}
	if c.SnoozeDays <= 0 {
		c.SnoozeDays = 90
	}
	if c.IntervalDays <= 0 {
		c.IntervalDays = 7
	}
	return c
}

// DigestText renders the notifier message for a group's idle items.
func DigestText(items []repo.XInvItem, days int, now time.Time, baseURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Homebox: %d item(s) untouched for %d+ days\n", len(items), days)
	for _, it := range items {
		age := int(now.Sub(it.LastTouchedAt).Hours() / 24)
		b.WriteString(" - " + it.Name)
		if it.LocationPath != "" {
			b.WriteString(" (" + it.LocationPath + ")")
		}
		b.WriteString(", " + strconv.Itoa(age) + " days\n")
	}
	b.WriteString("Keep, use, sell or archive")
	if baseURL != "" {
		b.WriteString(": " + strings.TrimRight(baseURL, "/") + "/x/idle")
	} else {
		b.WriteString(" them on the Idle items page.")
	}
	return b.String()
}

// DigestJob sends the idle digest to every group whose last digest is older
// than the configured interval.
type DigestJob struct {
	Repos   *repo.AllRepos
	Conf    config.IdleConf
	Notify  Notify
	BaseURL string
}

// Run is safe to call often (e.g. hourly); each group gets at most one
// digest per interval, tracked in x_idle_digests.
func (j *DigestJob) Run(ctx context.Context, now time.Time) error {
	conf := IdleSettings(j.Conf)
	if !j.Conf.Enabled {
		return nil
	}
	groups, err := j.Repos.Groups.GetAllGroups(ctx, uuid.Nil)
	if err != nil {
		return err
	}
	var errs []error
	for _, g := range groups {
		if err := j.runGroup(ctx, g.ID, conf, now); err != nil {
			errs = append(errs, fmt.Errorf("group %s: %w", g.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (j *DigestJob) runGroup(ctx context.Context, gid uuid.UUID, conf config.IdleConf, now time.Time) error {
	last, err := j.Repos.XUseBuild.LastDigest(ctx, gid)
	if err != nil {
		return err
	}
	if last != nil && now.Sub(*last) < time.Duration(conf.IntervalDays)*24*time.Hour {
		return nil
	}
	notifiers, err := j.Repos.Notifiers.GetActiveByGroup(ctx, gid)
	if err != nil {
		return err
	}
	if len(notifiers) == 0 {
		return nil // nothing to send through; try again next run
	}
	cutoff := now.Add(-time.Duration(conf.Days) * 24 * time.Hour)
	items, err := j.Repos.XUseBuild.IdleItems(ctx, gid, cutoff, now, conf.DigestLimit)
	if err != nil {
		return err
	}
	if len(items) > 0 {
		msg := DigestText(items, conf.Days, now, j.BaseURL)
		sent := 0
		for _, n := range notifiers {
			if err := j.Notify(n.URL, msg); err != nil {
				log.Warn().Err(err).Str("notifier", n.Name).Msg("idle digest: send failed")
				continue
			}
			sent++
		}
		if sent == 0 {
			return errors.New("idle digest: all notifiers failed")
		}
		ids := make([]uuid.UUID, len(items))
		for i, it := range items {
			ids[i] = it.ID
		}
		if err := j.Repos.XUseBuild.MarkNudged(ctx, gid, ids, now); err != nil {
			return err
		}
	}
	return j.Repos.XUseBuild.RecordDigest(ctx, gid, now, len(items))
}

const saleSystemPrompt = `You write a Mercari (メルカリ) listing draft in Japanese for a used item.
Reply with plain text in exactly this layout and nothing else:
タイトル: <max 40 characters>
説明:
<3-6 short lines: what it is, condition, what's included, notes; no invented specs>
状態: <one of 新品・未使用 / 未使用に近い / 目立った傷や汚れなし / やや傷や汚れあり>
希望価格: <yen amount>`

// SaleDraft returns a listing draft: model-written when chat is set,
// otherwise a template. The user copies it and lists manually.
func SaleDraft(ctx context.Context, chat aichat.Chatter, it repo.XInvItem) string {
	price := SuggestedPrice(it.PurchasePrice)
	if chat != nil {
		var b strings.Builder
		fmt.Fprintf(&b, "Item: %s\n", oneLine(it.Name, 200))
		if it.Description != "" {
			fmt.Fprintf(&b, "Description: %s\n", oneLine(it.Description, 600))
		}
		if it.PurchasePrice > 0 {
			fmt.Fprintf(&b, "Purchase price: ¥%.0f\n", it.PurchasePrice)
		}
		if it.PurchaseDate != nil {
			fmt.Fprintf(&b, "Purchased: %s\n", it.PurchaseDate.Format("2006-01"))
		}
		if price > 0 {
			fmt.Fprintf(&b, "Suggested price: ¥%d\n", price)
		}
		out, err := chat.Chat(ctx, saleSystemPrompt, b.String(), false)
		if err == nil && strings.TrimSpace(out) != "" {
			return strings.TrimSpace(out)
		}
		log.Warn().Err(err).Msg("sale draft: llm failed, using template")
	}
	return saleTemplate(it, price)
}

// SuggestedPrice is a rough second-hand price: 50% of the purchase price,
// rounded down to ¥100, minimum ¥300. 0 when the price is unknown.
func SuggestedPrice(purchase float64) int {
	if purchase <= 0 {
		return 0
	}
	p := int(purchase*0.5) / 100 * 100
	if p < 300 {
		p = 300
	}
	return p
}

func saleTemplate(it repo.XInvItem, price int) string {
	title := []rune(oneLine(it.Name, 200))
	if len(title) > 40 {
		title = title[:40]
	}
	var b strings.Builder
	b.WriteString("タイトル: " + string(title) + "\n説明:\n")
	b.WriteString(oneLine(it.Name, 200) + "です。\n")
	if it.Description != "" {
		b.WriteString(oneLine(it.Description, 600) + "\n")
	}
	if it.PurchaseDate != nil {
		b.WriteString(it.PurchaseDate.Format("2006年1月") + "頃に購入しました。\n")
	}
	b.WriteString("自宅保管品のため、神経質な方はご遠慮ください。\n")
	b.WriteString("状態: 目立った傷や汚れなし\n")
	if price > 0 {
		b.WriteString("希望価格: ¥" + strconv.Itoa(price))
	} else {
		b.WriteString("希望価格: ")
	}
	return b.String()
}
