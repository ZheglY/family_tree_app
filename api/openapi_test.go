package api_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/ZheglY/family_tree_app/internal/core/transport/http/server"
	authhttp "github.com/ZheglY/family_tree_app/internal/features/auth/transport"
	exporthttp "github.com/ZheglY/family_tree_app/internal/features/exports/transport"
	healthhttp "github.com/ZheglY/family_tree_app/internal/features/health/transport"
	mediahttp "github.com/ZheglY/family_tree_app/internal/features/media/transport"
	personhttp "github.com/ZheglY/family_tree_app/internal/features/persons/transport"
	relationhttp "github.com/ZheglY/family_tree_app/internal/features/relationships/transport"
	treehttp "github.com/ZheglY/family_tree_app/internal/features/trees/transport"
	unionhttp "github.com/ZheglY/family_tree_app/internal/features/unions/transport"
	"github.com/getkin/kin-openapi/openapi3"
)

func TestOpenAPIIsValidAndMatchesRegisteredRoutes(t *testing.T) {
	t.Parallel()
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	document, err := loader.LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatalf("load OpenAPI: %v", err)
	}
	if err := document.Validate(context.Background()); err != nil {
		t.Fatalf("validate OpenAPI: %v", err)
	}

	actual := registeredRoutes(t)
	documented := documentedRoutes(t, document)
	if missing := difference(actual, documented); len(missing) > 0 {
		t.Errorf("registered routes missing from OpenAPI: %s", strings.Join(missing, ", "))
	}
	if extra := difference(documented, actual); len(extra) > 0 {
		t.Errorf("OpenAPI routes not registered by the application: %s", strings.Join(extra, ", "))
	}
	for key, route := range actual {
		if operation, exists := documented[key]; exists && route.bearerProtected != operation.bearerProtected {
			t.Errorf("%s bearer security = %t, registered middleware = %t", key, operation.bearerProtected, route.bearerProtected)
		}
	}
}

type routeContract struct {
	bearerProtected bool
}

func registeredRoutes(t *testing.T) map[string]routeContract {
	t.Helper()
	routeGroups := []struct {
		prefix string
		routes []server.Route
	}{
		{routes: healthhttp.NewHealthHTTPHandler(nil).Routes()},
		{prefix: "/api/v1", routes: authhttp.NewHandler(nil, nil, nil, nil).Routes()},
		{prefix: "/api/v1", routes: treehttp.NewHandler(nil, nil).Routes()},
		{prefix: "/api/v1", routes: personhttp.NewHandler(nil, nil).Routes()},
		{prefix: "/api/v1", routes: relationhttp.NewHandler(nil, nil).Routes()},
		{prefix: "/api/v1", routes: unionhttp.NewHandler(nil, nil).Routes()},
		{prefix: "/api/v1", routes: mediahttp.NewHandler(nil, nil).Routes()},
		{prefix: "/api/v1", routes: exporthttp.NewHandler(nil, nil).Routes()},
	}

	result := make(map[string]routeContract)
	for _, group := range routeGroups {
		for _, route := range group.routes {
			key := route.Method + " " + group.prefix + route.Path
			if _, exists := result[key]; exists {
				t.Fatalf("duplicate registered route %s", key)
			}
			result[key] = routeContract{bearerProtected: len(route.Middleware) > 0}
		}
	}
	return result
}

func documentedRoutes(t *testing.T, document *openapi3.T) map[string]routeContract {
	t.Helper()
	result := make(map[string]routeContract)
	operationIDs := make(map[string]string)
	for path, item := range document.Paths.Map() {
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
			operation := item.GetOperation(method)
			if operation == nil {
				continue
			}
			key := method + " " + path
			if operation.OperationID == "" || operation.Summary == "" || len(operation.Tags) == 0 ||
				operation.Responses == nil || operation.Responses.Len() == 0 {
				t.Errorf("%s must define operationId, summary, tags and responses", key)
			}
			if previous, exists := operationIDs[operation.OperationID]; exists {
				t.Errorf("duplicate operationId %q on %s and %s", operation.OperationID, previous, key)
			}
			assertTypedSuccessResponses(t, key, operation)
			operationIDs[operation.OperationID] = key
			result[key] = routeContract{bearerProtected: usesBearerSecurity(operation)}
		}
	}
	return result
}

func assertTypedSuccessResponses(t *testing.T, route string, operation *openapi3.Operation) {
	t.Helper()
	for status, response := range operation.Responses.Map() {
		if !strings.HasPrefix(status, "2") || response.Value == nil {
			continue
		}
		mediaType := response.Value.Content.Get("application/json")
		if mediaType == nil {
			continue
		}
		if mediaType.Schema == nil || mediaType.Schema.Value == nil {
			t.Errorf("%s response %s must define a JSON schema", route, status)
			continue
		}
		schema := mediaType.Schema.Value
		if schema.Type != nil && schema.Type.Is("object") && len(schema.Properties) == 0 {
			t.Errorf("%s response %s uses an unconstrained JSON object", route, status)
		}
	}
}

func usesBearerSecurity(operation *openapi3.Operation) bool {
	if operation.Security == nil {
		return false
	}
	for _, requirement := range *operation.Security {
		if _, exists := requirement["bearerAuth"]; exists {
			return true
		}
	}
	return false
}

func difference(left map[string]routeContract, right map[string]routeContract) []string {
	result := make([]string, 0)
	for key := range left {
		if _, exists := right[key]; !exists {
			result = append(result, key)
		}
	}
	sort.Strings(result)
	return result
}
