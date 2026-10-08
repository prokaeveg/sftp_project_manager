package transport

import (
	"context"
	"os"
	"sort"
	"strings"
)

type processEnvKey struct{}

func withManagedEnvironment(ctx context.Context, values map[string]string) context.Context {
	return context.WithValue(ctx, processEnvKey{}, values)
}

func managedEnvironment(ctx context.Context) []string {
	values := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		normalized := strings.ToUpper(key)
		if managedEnvKey(normalized) {
			continue
		}
		values[normalized] = key + "=" + value
	}
	values["SSH_ASKPASS_REQUIRE"] = "SSH_ASKPASS_REQUIRE=never"
	if overrides, ok := ctx.Value(processEnvKey{}).(map[string]string); ok {
		for key, value := range overrides {
			values[strings.ToUpper(key)] = key + "=" + value
		}
	}
	env := make([]string, 0, len(values))
	for _, entry := range values {
		env = append(env, entry)
	}
	sort.Slice(env, func(i, j int) bool { return strings.ToUpper(env[i]) < strings.ToUpper(env[j]) })
	return env
}

func managedEnvKey(key string) bool {
	switch key {
	case "SSH_ASKPASS", "SSH_ASKPASS_REQUIRE", askpassFlagEnv, askpassAddrEnv, askpassTokenEnv:
		return true
	default:
		return false
	}
}
