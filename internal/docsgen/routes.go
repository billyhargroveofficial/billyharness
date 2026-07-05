package docsgen

import (
	"bytes"
	"reflect"
	"sort"

	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

type gatewayAPIReferenceData struct {
	DTOPackage string
	Routes     []gateway.RouteDoc
}

func GenerateGatewayAPI() ([]byte, error) {
	data := gatewayAPIReferenceInput()
	var b bytes.Buffer
	b.Write(header("internal/gateway"))
	b.WriteString("# Gateway API Reference\n\n")
	b.WriteString("Gateway security is enforced by one middleware: `/health` bypasses auth, `/v1/*` reads are local-read routes, and `/v1/*` mutations are bearer-mutation routes with an explicit loopback development bypass.\n\n")
	b.WriteString("DTO package: `" + data.DTOPackage + "`.\n\n")
	b.WriteString(markdownTable([]string{"Method", "Path", "Auth", "Request", "Response", "Summary"}, gatewayRouteRows(data.Routes)))
	footer, err := sourceHashFooter(data)
	if err != nil {
		return nil, err
	}
	b.Write(footer)
	return b.Bytes(), nil
}

func gatewayAPIReferenceInput() gatewayAPIReferenceData {
	routes := gateway.RouteDocs()
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Pattern == routes[j].Pattern {
			return routes[i].Method < routes[j].Method
		}
		return routes[i].Pattern < routes[j].Pattern
	})
	return gatewayAPIReferenceData{
		DTOPackage: reflect.TypeOf(gatewayapi.RunRequest{}).PkgPath(),
		Routes:     routes,
	}
}

func gatewayRouteRows(routes []gateway.RouteDoc) [][]string {
	rows := make([][]string, 0, len(routes))
	for _, route := range routes {
		rows = append(rows, []string{
			route.Method,
			route.Pattern,
			route.AuthClass,
			route.Request,
			route.Response,
			route.Summary,
		})
	}
	return rows
}
