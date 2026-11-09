package endpoints

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// lines is a read's endpoints as "kind api method path direction".
func lines(r Read) []string {
	var out []string
	for _, e := range r.Endpoints {
		out = append(out, strings.Join([]string{e.Kind, e.API, e.Method, e.Path, e.Direction}, " "))
	}
	return out
}

func apis(r Read) []string {
	var out []string
	for _, a := range r.APIs {
		out = append(out, strings.Join([]string{a.Kind, a.Name, a.BasePath, a.Direction}, " "))
	}
	return out
}

func eq(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got  %q\n want %q", what, got, want)
	}
}

func TestSpring(t *testing.T) {
	r := Parse(map[string][]byte{
		"orders/src/main/resources/application.yml": []byte("server:\n  servlet:\n    context-path: /shop\napp:\n  base: /v1\n"),
		"orders/src/main/java/shop/Paths.java":      []byte(`class Paths { public static final String ORDERS = "/orders"; }`),
		"orders/src/main/java/shop/OrderController.java": []byte(`package shop;
@RestController
@RequestMapping("${app.base}" + Paths.ORDERS)
public class OrderController {
    @GetMapping
    public List<Order> list() { return null; }
    @GetMapping("/{id:\\d+}")
    public Order get(@PathVariable String id) { return null; }
    @PostMapping(value = {"", "/bulk"}, consumes = "application/json")
    public void create(@RequestBody Order o) {}
    @RequestMapping(path = "/{id}", method = {RequestMethod.PUT, RequestMethod.PATCH})
    public void update() {}
    @RequestMapping("/any")
    public void any() {}
    @GetMapping(PARAM)
    public void unresolved() {}
    // @GetMapping("/commented")
}`),
		"orders/src/main/java/shop/Client.java": []byte(`@FeignClient(name = "stock")
interface Client {
    @GetMapping("/stock/{id}")
    Stock get(@PathVariable String id);
}`),
		"orders/src/main/java/shop/Service.java": []byte(`@Service
class Service {
    @GetMapping("/not-a-controller")
    void x() {}
}`),
	})
	eq(t, "endpoints", lines(r), []string{
		"http  GET /shop/v1/orders offers",
		"http  POST /shop/v1/orders offers",
		"http  * /shop/v1/orders/any offers",
		"http  POST /shop/v1/orders/bulk offers",
		"http  GET /shop/v1/orders/{id} offers",
		"http  PATCH /shop/v1/orders/{id} offers",
		"http  PUT /shop/v1/orders/{id} offers",
	})
	if r.Endpoints[0].Summary != "list" || r.Endpoints[0].Files[0] != "orders/src/main/java/shop/OrderController.java" {
		t.Errorf("handler or file: %+v", r.Endpoints[0])
	}
}

func TestJAXRSAndInterface(t *testing.T) {
	r := Parse(map[string][]byte{
		"src/main/java/a/App.java": []byte(`@ApplicationPath("/api") public class App extends Application {}`),
		"src/main/java/a/Items.java": []byte(`@Path("items")
public class Items {
    @GET
    public List<Item> all() {}
    @GET @Path("{id}")
    public Item one(@PathParam("id") String id) {}
    @Path("sub")
    public Sub locator() {}
}`),
		"src/main/java/a/UsersApi.java": []byte(`@RequestMapping("/users")
public interface UsersApi {
    @GetMapping("/{id}")
    User get(@PathVariable String id);
}`),
		"src/main/java/a/UsersController.java": []byte(`@RestController
public class UsersController implements UsersApi {
    public User get(String id) { return null; }
}`),
		"src/main/java/a/Remote.java": []byte(`@RegisterRestClient
@Path("/remote")
public interface Remote { @GET String hi(); }`),
		"src/main/java/a/RemoteUser.java": []byte(`class RemoteUser implements Remote { }`),
	})
	eq(t, "endpoints", lines(r), []string{
		"http  GET /api/items offers",
		"http  GET /api/items/{id} offers",
		"http  GET /users/{id} offers",
	})
	users := r.Endpoints[2]
	if !reflect.DeepEqual(users.Files, []string{"src/main/java/a/UsersApi.java", "src/main/java/a/UsersController.java"}) {
		t.Errorf("interface files: %v", users.Files)
	}
}

func TestNestExpressGo(t *testing.T) {
	r := Parse(map[string][]byte{
		"api/src/main.ts": []byte(`const app = await NestFactory.create(AppModule); app.setGlobalPrefix('api');`),
		"api/src/orders.controller.ts": []byte(`import { Controller, Get, Post } from '@nestjs/common';
@Controller('orders')
export class OrdersController {
  @Get(':id')
  findOne(@Param('id') id: string) {}
  @Post()
  create() {}
}`),
		"web/server.js": []byte(`const express = require('express');
const app = express();
const router = express.Router();
const other = express.Router();
app.get('/health', (req, res) => res.send('ok'));
router.post('/login', h);
other.get('/lost', h);
app.use('/auth', router);
fetch.get('/not-a-route');`),
		"svc/main.go": []byte(`package main
const prefix = "/v2"
func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", get)
	http.Handle("/metrics", h)
	r := gin.Default()
	v := r.Group(prefix)
	v.POST("/items", create)
	resp, _ := http.Get("https://example.com/x")
	http.ListenAndServe(":8080", mux)
}
func routes(g *gin.RouterGroup) { g.GET("/unknown-prefix", h) }`),
	})
	eq(t, "endpoints", lines(r), []string{
		"http  POST /api/orders offers",
		"http  GET /api/orders/{id} offers",
		"http  POST /auth/login offers",
		"http  GET /health offers",
		"http  GET /items/{id} offers",
		"http  * /metrics offers",
		"http  POST /v2/items offers",
	})
}

const ordersSpec = `openapi: 3.0.1
info:
  title: Orders API
  version: 2.1.0
servers:
  - url: https://{host}/orders-service/v2
    variables:
      host:
        default: example.com
paths:
  /orders:
    get:
      summary: List
        orders
    post:
      operationId: createOrder
  /orders/{orderId}:
    $ref: ./paths/order.yaml
`

func TestSpecOffersByRoute(t *testing.T) {
	r := Parse(map[string][]byte{
		"api/orders.yaml":      []byte(ordersSpec),
		"api/paths/order.yaml": []byte("get:\n  summary: One\ndelete:\n  summary: Gone\n"),
		"src/main/java/OrderController.java": []byte(`@RestController
@RequestMapping("/orders")
class OrderController {
    @GetMapping
    List<Order> list() {}
    @GetMapping("/{id}")
    Order one() {}
    @GetMapping("/extra")
    void extra() {}
}`),
	})
	eq(t, "apis", apis(r), []string{"http Orders API /orders-service/v2 offers", "http   offers"})
	eq(t, "endpoints", lines(r), []string{
		"http Orders API GET /orders-service/v2/orders offers",
		"http Orders API POST /orders-service/v2/orders offers",
		"http Orders API DELETE /orders-service/v2/orders/{orderId} offers",
		"http Orders API GET /orders-service/v2/orders/{orderId} offers",
		"http  GET /orders/extra offers",
	})
	var list Endpoint
	for _, e := range r.Endpoints {
		if e.Method == "GET" && e.Path == "/orders-service/v2/orders" {
			list = e
		}
	}
	if list.Summary != "List orders" || len(list.Files) != 2 {
		t.Errorf("merged endpoint: %+v", list)
	}
	if r.APIs[0].Version != "2.1.0" {
		t.Errorf("version: %+v", r.APIs[0])
	}
}

func TestSpecDirectionByGenerator(t *testing.T) {
	pom := `<project><build><plugins>
<plugin><artifactId>openapi-generator-maven-plugin</artifactId><executions>
<execution><id>server</id><configuration><inputSpec>${project.basedir}/src/main/resources/own.yaml</inputSpec><generatorName>spring</generatorName></configuration></execution>
<execution><id>client</id><configuration><inputSpec>${project.basedir}/specs/stock.yaml</inputSpec><generatorName>spring</generatorName><library>spring-cloud</library></configuration></execution>
</executions></plugin></plugins></build></project>`
	spec := func(title string) []byte {
		return []byte("swagger: '2.0'\ninfo:\n  title: " + title + "\nbasePath: /\npaths:\n  /x:\n    get: {}\n")
	}
	r := Parse(map[string][]byte{
		"pom.xml":                       []byte(pom),
		"src/main/resources/own.yaml":   spec("Own"),
		"specs/stock.yaml":              spec("Stock"),
		"docs/partner.json":             []byte(`{"openapi":"3.1.0","info":{"title":"Partner"},"paths":{"/p":{"get":{}}}}`),
		"src/main/resources/other.yaml": []byte("openapi: 3.0.0\ninfo: {title: Nope}\n"),
	})
	eq(t, "apis", apis(r), []string{"http Own  offers", "http Partner  declares", "http Stock  declares"})
}

func TestGradleAndCLIGenerators(t *testing.T) {
	got := generators(map[string][]byte{
		"build.gradle.kts": []byte(`openApiGenerate {
    generatorName.set("kotlin-spring")
    inputSpec.set("$rootDir/api/a.yaml")
}
tasks.register<GenerateTask>("client") {
    generatorName.set("kotlin")
    inputSpec.set("$rootDir/api/b.yaml")
}`),
		"package.json":      []byte(`{"scripts":{"gen":"openapi-generator-cli generate -i specs/c.yaml -g typescript-angular -o src/gen"}}`),
		"openapitools.json": []byte(`{"generator-cli":{"generators":{"srv":{"generatorName":"go-server","inputSpec":"d.yaml"}}}}`),
	})
	specs := []string{"api/a.yaml", "api/b.yaml", "specs/c.yaml", "web/d.yaml"}
	for _, c := range []struct {
		spec string
		want bool
	}{{"api/a.yaml", true}, {"api/b.yaml", false}, {"specs/c.yaml", false}, {"web/d.yaml", true}} {
		if serverGenerated(got, c.spec, specs) != c.want {
			t.Errorf("serverGenerated(%s) = %v", c.spec, !c.want)
		}
	}
}

// A generator names its spec by path: two specs of one file name, the
// repository's own and a partner's, are told apart by their folders; a
// name alone names one spec of that name, never one of two.
func TestGeneratorBySpecPath(t *testing.T) {
	pom := `<project><build><plugins><plugin><artifactId>openapi-generator-maven-plugin</artifactId><executions>
<execution><configuration><inputSpec>${project.basedir}/src/main/resources/api/openapi.yaml</inputSpec><generatorName>spring</generatorName></configuration></execution>
<execution><configuration><inputSpec>${project.basedir}/src/main/resources/clients/pay/openapi.yaml</inputSpec><generatorName>java</generatorName></configuration></execution>
</executions></plugin></plugins></build></project>`
	gens := generators(map[string][]byte{"orders/pom.xml": []byte(pom)})
	specs := []string{"orders/src/main/resources/api/openapi.yaml", "orders/src/main/resources/clients/pay/openapi.yaml"}
	if !serverGenerated(gens, specs[0], specs) || serverGenerated(gens, specs[1], specs) {
		t.Error("own spec and partner's spec of one name not told apart")
	}
	bare := []gen{{build: "package.json", input: "openapi.yaml", serves: true}}
	if serverGenerated(bare, specs[0], specs) {
		t.Error("a name alone named one of two specs of that name")
	}
	if !serverGenerated(bare, specs[0], specs[:1]) {
		t.Error("a name alone missed the one spec of that name")
	}
}

// One shared route makes no partner's spec this repository's: under half
// its operations served, it declares, and the route stays the code's own.
// Two specs of one title and two versions stay two APIs; two interfaces
// sharing an operation keep it each.
func TestSpecIdentityAndHalf(t *testing.T) {
	partner := []byte("openapi: 3.0.0\ninfo: {title: Partner}\npaths:\n  /health: {get: {}}\n  /a: {get: {}}\n  /b: {get: {}}\n")
	v := func(ver string) []byte {
		return []byte("openapi: 3.0.0\ninfo: {title: Orders, version: '" + ver + "'}\npaths:\n  /health: {get: {}}\n")
	}
	r := Parse(map[string][]byte{
		"specs/partner.yaml": partner,
		"specs/v1.yaml":      v("1"),
		"specs/v2.yaml":      v("2"),
		"src/main/java/H.java": []byte(`@RestController class H {
    @GetMapping("/health") String h() {}
}`),
	})
	eq(t, "apis", apis(r), []string{"http Orders 1  offers", "http Orders 2  offers", "http Partner  declares"})
	eq(t, "endpoints", lines(r), []string{
		"http Orders 1 GET /health offers",
		"http Orders 2 GET /health offers",
		"http Partner GET /a declares",
		"http Partner GET /b declares",
		"http Partner GET /health declares",
	})

	// Two SOAP services of one WSDL each keep the file's operations.
	two := strings.Replace(wsdl, `</wsdl:definitions>`, `<wsdl:service name="Other"/></wsdl:definitions>`, 1)
	s := Parse(map[string][]byte{"b.wsdl": []byte(two)})
	if len(s.Endpoints) != 4 {
		t.Errorf("two services, one WSDL: %q", lines(s))
	}
}

// A type inside a controller (a DTO record, an annotated request class)
// leaves its routes alone; a context path is the module's own.
func TestNestedTypesAndModules(t *testing.T) {
	r := Parse(map[string][]byte{
		"a/src/main/resources/application.yml": []byte("server:\n  servlet:\n    context-path: /a\n"),
		"b/src/main/java/OrderController.java": []byte(`@RestController
@RequestMapping("/orders")
class OrderController {
    @Schema(description = "x")
    record OrderDto(String id) {}
    @Data
    static class Request { String x; }
    @GetMapping("/{id}")
    Order get() {}
}
@RestController
class Other {
    @GetMapping("/other")
    void o() {}
}`),
	})
	eq(t, "endpoints", lines(r), []string{"http  GET /orders/{id} offers", "http  GET /other offers"})
}

// A Maven plugin's own configuration names the spec when its execution
// names only the goal.
func TestPomPluginLevelConfiguration(t *testing.T) {
	gens := generators(map[string][]byte{"pom.xml": []byte(`<project><build><plugins><plugin>
<artifactId>openapi-generator-maven-plugin</artifactId>
<configuration><inputSpec>${project.basedir}/api.yaml</inputSpec><generatorName>spring</generatorName></configuration>
<executions><execution><goals><goal>generate</goal></goals></execution></executions>
</plugin></plugins></build></project>`)})
	if !serverGenerated(gens, "api.yaml", []string{"api.yaml"}) {
		t.Errorf("plugin-level configuration not read: %+v", gens)
	}
}

// A chi router nested by closure hides its routes' prefix: none of them
// is read, Handle included; the default mux's are.
func TestGoNestedRouter(t *testing.T) {
	r := Parse(map[string][]byte{"main.go": []byte(`package main
func main() {
	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Handle("/orders", h)
		r.Get("/items", h)
	})
	http.HandleFunc("/health", h)
	http.ListenAndServe(":8080", r)
}`)})
	eq(t, "endpoints", lines(r), []string{"http  * /health offers"})
}

// An interface's routes sit under the context path of the module whose
// controller serves them, not the interface's.
func TestInterfaceRoutesUnderTheControllersModule(t *testing.T) {
	r := Parse(map[string][]byte{
		"service/src/main/resources/application.yml": []byte("server:\n  servlet:\n    context-path: /shop\n"),
		"api/src/main/java/OrdersApi.java":           []byte(`@RequestMapping("/orders") public interface OrdersApi { @GetMapping Order list(); }`),
		"service/src/main/java/Orders.java":          []byte(`@RestController class Orders implements OrdersApi { public Order list() {} }`),
	})
	eq(t, "endpoints", lines(r), []string{"http  GET /shop/orders offers"})
}

// A path item's $ref chain is followed file by file, and a spec with no
// $ref to another file is not decoded for it; lock files are no specs.
func TestRefChain(t *testing.T) {
	files := map[string][]byte{
		"api/main.yaml":     []byte("openapi: 3.0.0\ninfo: {title: Chain}\npaths:\n  /a:\n    $ref: ./paths/a.yaml\n"),
		"api/paths/a.yaml":  []byte("$ref: ../ops/a.yaml#/item\n"),
		"api/inline.yaml":   []byte("openapi: 3.0.0\npaths:\n  /x:\n    $ref: '#/components/x'\n"),
		"package-lock.json": []byte(`{"swagger-ui": "1"}`),
	}
	eq(t, "refs", Refs(files), []string{"api/ops/a.yaml"})
	files["api/ops/a.yaml"] = []byte("item:\n  get: {summary: Deep}\n")
	if got := Refs(files); len(got) != 0 {
		t.Errorf("refs after the chain was read: %q", got)
	}
	r := Parse(files)
	if len(r.Endpoints) != 1 || r.Endpoints[0].Summary != "Deep" {
		t.Errorf("the chain's operation: %+v", r.Endpoints)
	}
	if SpecCandidate("web/package-lock.json") || !SpecCandidate("api/openapi.json") {
		t.Error("lock files are no specs; a spec is")
	}
}

// A Go router built where nothing serves it (a package that returns its
// routes for another to mount) is left out: its prefix is another file's.
func TestGoRouterNotServed(t *testing.T) {
	r := Parse(map[string][]byte{"orders/routes.go": []byte(`package orders
func Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/{id}", get)
	return r
}`)})
	if len(r.Endpoints) != 0 {
		t.Errorf("routes of a router mounted elsewhere: %q", lines(r))
	}
}

// The grep finds what Parse needs, POSIX alone: a route, a constants file
// of paths; not a file that merely says express or handles something.
func TestGrepPattern(t *testing.T) {
	re := regexp.MustCompile("(?i)(" + GrepPattern + ")")
	for _, s := range []string{
		`@GetMapping("/x")`, `@GET`, `@Path("x")`, `public static final String ORDERS = "/orders";`,
		`export const BASE = '/api';`, `const app = require('express')();`, `import express from "express";`,
		`mux.HandleFunc("/x", h)`, `r.Get("/x", h)`, `class X extends OrdersGrpc.OrdersImplBase`, `@PayloadRoot(localPart = "X")`,
	} {
		if !re.MatchString(s) {
			t.Errorf("grep misses %q", s)
		}
	}
	for _, s := range []string{`// an expression`, `handler.handle(event)`, `@Endpoint(id = "info")`, `@GetterX`} {
		if re.MatchString(s) {
			t.Errorf("grep takes %q", s)
		}
	}
}

func TestGRPC(t *testing.T) {
	proto := []byte(`syntax = "proto3";
package shop.v1;
// service Fake { rpc Nope() }
service Orders {
  rpc Get (GetRequest) returns (Order);
  rpc List (ListRequest) returns (stream Order);
}
service Stock {
  rpc Check (CheckRequest) returns (Level);
}`)
	r := Parse(map[string][]byte{
		"proto/shop.proto": proto,
		"src/main/java/OrdersImpl.java": []byte(`@GrpcService
public class OrdersImpl extends OrdersGrpc.OrdersImplBase { }`),
	})
	eq(t, "apis", apis(r), []string{"grpc shop.v1.Orders  offers", "grpc shop.v1.Stock  declares"})
	eq(t, "endpoints", lines(r), []string{
		"grpc shop.v1.Orders RPC /shop.v1.Orders/Get offers",
		"grpc shop.v1.Orders RPC /shop.v1.Orders/List offers",
		"grpc shop.v1.Stock RPC /shop.v1.Stock/Check declares",
	})
	g := Parse(map[string][]byte{"api.proto": proto, "main.go": []byte("pb.RegisterStockServer(s, &srv{})")})
	eq(t, "go", apis(g), []string{"grpc shop.v1.Orders  declares", "grpc shop.v1.Stock  offers"})
}

const wsdl = `<?xml version="1.0"?>
<wsdl:definitions name="Billing" xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/" xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/">
  <wsdl:portType name="BillingPort">
    <wsdl:operation name="CreateInvoice"/>
    <wsdl:operation name="CancelInvoice"/>
  </wsdl:portType>
  <wsdl:binding name="B" type="tns:BillingPort">
    <wsdl:operation name="CreateInvoice"/>
  </wsdl:binding>
  <wsdl:service name="BillingService">
    <wsdl:port name="P" binding="tns:B">
      <soap:address location="http://localhost:8080/ws/billing"/>
    </wsdl:port>
  </wsdl:service>
</wsdl:definitions>`

func TestSOAP(t *testing.T) {
	r := Parse(map[string][]byte{
		"src/main/resources/billing.wsdl": []byte(wsdl),
		"src/main/java/Config.java": []byte(`@Bean public Wsdl11Definition billing() {
    return new SimpleWsdl11Definition(new ClassPathResource("billing.wsdl"));
}`),
	})
	eq(t, "apis", apis(r), []string{"soap BillingService /ws/billing offers"})
	eq(t, "endpoints", lines(r), []string{
		"soap BillingService SOAP CancelInvoice offers",
		"soap BillingService SOAP CreateInvoice offers",
	})
	c := Parse(map[string][]byte{"billing.wsdl": []byte(wsdl)})
	eq(t, "client", apis(c), []string{"soap BillingService /ws/billing declares"})
	// An Actuator endpoint is no SOAP server.
	a := Parse(map[string][]byte{"billing.wsdl": []byte(wsdl),
		"src/main/java/Info.java": []byte(`@Endpoint(id = "info") public class Info { @ReadOperation String x() {} }`)})
	eq(t, "actuator", apis(a), []string{"soap BillingService /ws/billing declares"})
	ws := Parse(map[string][]byte{"billing.wsdl": []byte(wsdl),
		"src/main/java/Ws.java": []byte(`@Endpoint public class Ws { @PayloadRoot(localPart = "X") void x() {} }`)})
	eq(t, "spring-ws", apis(ws), []string{"soap BillingService /ws/billing offers"})
}

func TestWSDLNamed(t *testing.T) {
	got := WSDLNamed([]string{"a/Order.wsdl", "a/CustomerOrder.wsdl", "vendor/Other.wsdl"},
		map[string][]byte{"pom.xml": []byte("<wsdlFile>customerorder.wsdl</wsdlFile>")})
	eq(t, "named", got, []string{"a/CustomerOrder.wsdl"})
}

func TestNormalizeAndMatch(t *testing.T) {
	for in, want := range map[string]string{
		"orders/:id/":           "/orders/{id}",
		"//a/<int:pk>?x=1":      "/a/{pk}",
		"/a/{id:\\d+}/b":        "/a/{id}/b",
		"":                      "/",
		"/files/{path...}":      "/files/{path...}",
		"/v1/items/{itemId}/x/": "/v1/items/{itemId}/x",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	if Key(HTTP, "GET", "/a/{id}") != Key(HTTP, "GET", "/a/:other") {
		t.Error("keys differ by parameter name")
	}
	for _, c := range []struct {
		route, path string
		want        bool
	}{
		{"/orders/{id}", "/orders/42", true},
		{"/orders/{id}", "/orders/", false},
		{"/orders/{id}", "/orders/42/items", false},
		{"/orders", "/orders/", true},
		{"/orders/:id/items", "/orders/7/items", true},
	} {
		if got := Match(c.route, c.path); got != c.want {
			t.Errorf("Match(%q, %q) = %v", c.route, c.path, got)
		}
	}
}

func TestRefs(t *testing.T) {
	got := Refs(map[string][]byte{"api/orders.yaml": []byte(ordersSpec), "api/x.yaml": []byte("a: 1")})
	eq(t, "refs", got, []string{"api/paths/order.yaml"})
}
