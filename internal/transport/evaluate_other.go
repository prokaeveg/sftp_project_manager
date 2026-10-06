//go:build !windows

package transport

import (
	"context"
	"errors"
)

func runEvaluation(ctx context.Context, executable string, args []string) (evaluationResult, error) {
	return evaluationResult{}, errors.New("проверка настроек с управлением дочерними процессами доступна только в Windows-сборке")
}
