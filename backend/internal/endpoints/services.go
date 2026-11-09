package endpoints

import (
	"bytes"
	"encoding/xml"
	"path"
	"regexp"
	"strings"
)

// service is a gRPC or SOAP service a contract file defines: its kind,
// its full name, its bare name (what code implementing it names), its
// base path (a SOAP address's), its operations.
type service struct {
	kind, name, bare, file, basePath string
	ops                              []op
}

var (
	protoPkg     = regexp.MustCompile(`(?m)^\s*package\s+([A-Za-z0-9_.]+)\s*;`)
	protoService = regexp.MustCompile(`(?ms)^\s*service\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{(.*?)^\s*\}`)
	protoRPC     = regexp.MustCompile(`\brpc\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
)

// protoServices reads a .proto's services and their methods, each method
// as gRPC addresses it: /package.Service/Method.
func protoServices(p string, body []byte) []service {
	src := stripProtoComments(string(body))
	pkg := ""
	if m := protoPkg.FindStringSubmatch(src); m != nil {
		pkg = m[1] + "."
	}
	var out []service
	for _, m := range protoService.FindAllStringSubmatch(src, -1) {
		s := service{kind: GRPC, name: pkg + m[1], bare: m[1], file: p}
		for _, r := range protoRPC.FindAllStringSubmatch(m[2], -1) {
			s.ops = append(s.ops, op{method: RPC, path: "/" + s.name + "/" + r[1]})
		}
		out = append(out, s)
	}
	return out
}

var protoComment = regexp.MustCompile(`(?s)//[^\n]*|/\*.*?\*/`)

func stripProtoComments(s string) string { return protoComment.ReplaceAllString(s, "") }

// wsdlServices reads a WSDL 1.1's services: each service's name and the
// path of its SOAP address, with every operation of the file's port
// types. A WSDL without a service (an abstract one) is one service by the
// definitions' name, else the file's.
func wsdlServices(p string, body []byte) []service {
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = false
	var stack []string
	var defName string
	var ops []op
	seenOp := map[string]bool{}
	type svc struct{ name, location string }
	var svcs []svc
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			attr := func(name string) string {
				for _, a := range t.Attr {
					if a.Name.Local == name {
						return a.Value
					}
				}
				return ""
			}
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			switch t.Name.Local {
			case "definitions":
				defName = attr("name")
			case "operation":
				if parent == "portType" {
					if n := attr("name"); n != "" && !seenOp[n] {
						seenOp[n] = true
						ops = append(ops, op{method: SOAPOp, path: n})
					}
				}
			case "service":
				svcs = append(svcs, svc{name: attr("name")})
			case "address":
				if parent == "port" && len(svcs) > 0 && svcs[len(svcs)-1].location == "" {
					svcs[len(svcs)-1].location = attr("location")
				}
			}
			stack = append(stack, t.Name.Local)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if len(svcs) == 0 {
		name := defName
		if name == "" {
			name = strings.TrimSuffix(path.Base(p), path.Ext(p))
		}
		svcs = []svc{{name: name}}
	}
	var out []service
	for _, s := range svcs {
		if s.name == "" {
			continue
		}
		out = append(out, service{kind: SOAP, name: s.name, bare: s.name, file: p, basePath: basePath(serverPath(s.location, nil)),
			ops: ops})
	}
	return out
}

// implementations are what a repository's code implements of the services
// its contracts define: gRPC services by bare name, SOAP services by name
// or by their WSDL's base name, and the files that say so; soapServer the
// files that serve SOAP without naming what (a Spring-WS endpoint).
type implementations struct {
	grpc       map[string][]string
	soap       map[string][]string
	soapServer []string
	// wsdls is how many WSDL files the repository's contracts hold: a
	// server that names none serves the one, never one of several.
	wsdls int
}

var (
	jvmGRPC  = regexp.MustCompile(`(?:extends|:)\s*[A-Za-z0-9_.]*?\b([A-Za-z_][A-Za-z0-9_]*?)(?:Coroutine)?ImplBase\b`)
	goGRPC   = regexp.MustCompile(`\bRegister([A-Za-z_][A-Za-z0-9_]*)Server\s*\(`)
	nestGRPC = regexp.MustCompile(`@GrpcMethod\s*\(\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]`)
	nodeGRPC = regexp.MustCompile(`\.addService\s*\(\s*[A-Za-z0-9_.]*?\.?([A-Za-z_][A-Za-z0-9_]*)\.service\b`)
	// A bare @Endpoint is no mark: Actuator's custom endpoints wear it too;
	// Spring-WS's handler methods wear @PayloadRoot.
	soapMark   = regexp.MustCompile(`@WebService\b[^\n]*\n?[^{]*\bclass\b|@PayloadRoot\b|Wsdl11Definition|<jaxws:endpoint\b|\bEndpointImpl\s*\(|\bEndpoint\.publish\s*\(`)
	soapName   = regexp.MustCompile(`serviceName\s*=\s*"([^"]+)"`)
	wsdlInCode = regexp.MustCompile(`([A-Za-z0-9_.\-]+\.wsdl)\b`)
)

// learn reads a source's implementations; src without comments.
func (im *implementations) learn(p, src string) {
	add := func(m map[string][]string, name string) map[string][]string {
		if m == nil {
			m = map[string][]string{}
		}
		m[name] = append(m[name], p)
		return m
	}
	for _, re := range []*regexp.Regexp{jvmGRPC, goGRPC, nestGRPC, nodeGRPC} {
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			im.grpc = add(im.grpc, m[1])
		}
	}
	if !soapMark.MatchString(src) {
		return
	}
	named := false
	for _, m := range soapName.FindAllStringSubmatch(src, -1) {
		im.soap, named = add(im.soap, m[1]), true
	}
	for _, m := range wsdlInCode.FindAllStringSubmatch(src, -1) {
		im.soap, named = add(im.soap, strings.ToLower(path.Base(m[1]))), true
	}
	if !named {
		im.soapServer = append(im.soapServer, p)
	}
}

// direction is whether the code serves a service, and the files that say
// so.
func (im implementations) direction(s service) (string, []string) {
	switch s.kind {
	case GRPC:
		if fs := im.grpc[s.bare]; len(fs) > 0 {
			return Offers, fs
		}
	case SOAP:
		var fs []string
		fs = append(fs, im.soap[s.name]...)
		fs = append(fs, im.soap[strings.ToLower(path.Base(s.file))]...)
		if len(fs) > 0 {
			return Offers, fs
		}
		if im.wsdls == 1 && len(im.soapServer) > 0 {
			return Offers, im.soapServer
		}
	}
	return Declares, nil
}
