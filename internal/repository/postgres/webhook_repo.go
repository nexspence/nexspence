package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

type webhookRepo struct {
	db *pgxpool.Pool
}

func NewWebhookRepo(db *pgxpool.Pool) *webhookRepo {
	return &webhookRepo{db: db}
}

func scanWebhook(row pgx.Row) (*domain.Webhook, error) {
	var w domain.Webhook
	var events []string
	err := row.Scan(&w.ID, &w.Name, &w.URL, &w.Secret, &events, &w.Active, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return nil, err
	}
	w.Events = make([]domain.WebhookEvent, len(events))
	for i, e := range events {
		w.Events[i] = domain.WebhookEvent(e)
	}
	return &w, nil
}

func (r *webhookRepo) List(ctx context.Context) ([]domain.Webhook, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, name, url, secret, events, active, created_at, updated_at FROM webhooks ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Webhook
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

func (r *webhookRepo) Get(ctx context.Context, id string) (*domain.Webhook, error) {
	row := r.db.QueryRow(ctx,
		`SELECT id, name, url, secret, events, active, created_at, updated_at FROM webhooks WHERE id = $1`, id)
	w, err := scanWebhook(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // (nil, nil) signals not-found; callers check the returned value
	}
	return w, err
}

func (r *webhookRepo) ListByEvent(ctx context.Context, event domain.WebhookEvent) ([]domain.Webhook, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, name, url, secret, events, active, created_at, updated_at
		   FROM webhooks WHERE active = true AND $1 = ANY(events) ORDER BY name`,
		string(event))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Webhook
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

func (r *webhookRepo) Create(ctx context.Context, w *domain.Webhook) error {
	events := make([]string, len(w.Events))
	for i, e := range w.Events {
		events[i] = string(e)
	}
	return r.db.QueryRow(ctx,
		`INSERT INTO webhooks (name, url, secret, events, active)
		   VALUES ($1, $2, $3, $4, $5)
		   RETURNING id, created_at, updated_at`,
		w.Name, w.URL, w.Secret, events, w.Active,
	).Scan(&w.ID, &w.CreatedAt, &w.UpdatedAt)
}

func (r *webhookRepo) Update(ctx context.Context, w *domain.Webhook) error {
	events := make([]string, len(w.Events))
	for i, e := range w.Events {
		events[i] = string(e)
	}
	return r.db.QueryRow(ctx,
		`UPDATE webhooks SET name=$1, url=$2, secret=$3, events=$4, active=$5, updated_at=now()
		   WHERE id=$6
		   RETURNING updated_at`,
		w.Name, w.URL, w.Secret, events, w.Active, w.ID,
	).Scan(&w.UpdatedAt)
}

func (r *webhookRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM webhooks WHERE id = $1`, id)
	return err
}
