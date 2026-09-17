package postgres

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexspence-oss/nexspence/internal/repository"
)

func TestForeignKeyViolation_ReportsConstraint(t *testing.T) {
	err := &pgconn.PgError{Code: pgerrForeignKeyViolation, ConstraintName: "assets_blob_store_id_fkey"}
	constraint, ok := foreignKeyViolation(err)
	if !ok {
		t.Fatal("want a foreign-key violation")
	}
	if constraint != "assets_blob_store_id_fkey" {
		t.Fatalf("constraint = %q", constraint)
	}
	if _, ok := foreignKeyViolation(fmt.Errorf("delete blob store: %w", err)); !ok {
		t.Fatal("errors.As must see a wrapped PgError")
	}
}

func TestForeignKeyViolation_IgnoresUniqueAndPlain(t *testing.T) {
	unique := &pgconn.PgError{Code: pgerrUniqueViolation, ConstraintName: "blob_stores_name_key"}
	if _, ok := foreignKeyViolation(unique); ok {
		t.Fatal("a unique violation is not a foreign-key violation")
	}
	if _, ok := foreignKeyViolation(errors.New("nope")); ok {
		t.Fatal("a plain error is not a foreign-key violation")
	}
}

func TestTranslateInUse_ConvertsForeignKey(t *testing.T) {
	err := &pgconn.PgError{Code: pgerrForeignKeyViolation, ConstraintName: "repositories_blob_store_id_fkey"}
	got := translateInUse(err)
	if !errors.Is(got, repository.ErrInUse) {
		t.Fatalf("translateInUse: want ErrInUse, got %v", got)
	}
	var inUse *repository.InUseError
	if !errors.As(got, &inUse) || inUse.Constraint != "repositories_blob_store_id_fkey" {
		t.Fatalf("translateInUse: want constraint named, got %#v", got)
	}
	plain := errors.New("disk full")
	if got := translateInUse(plain); !errors.Is(got, plain) {
		t.Fatal("translateInUse must pass unrelated errors through")
	}
}
