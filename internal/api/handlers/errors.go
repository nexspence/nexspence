package handlers

import (
	"errors"

	"github.com/nexspence-oss/nexspence/internal/service"
)

func isNotFound(err error) bool {
	return errors.Is(err, service.ErrNotFound)
}

func isAlreadyExists(err error) bool {
	return errors.Is(err, service.ErrAlreadyExists)
}

func isInvalidInput(err error) bool {
	return errors.Is(err, service.ErrInvalidInput)
}
