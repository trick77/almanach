package messaging

import (
	"reflect"
	"testing"
)

func topics(r Read) []string {
	var out []string
	for _, t := range r.Topics {
		out = append(out, t.Name+" "+t.Direction)
	}
	return out
}

func TestParseJVM(t *testing.T) {
	files := map[string][]byte{
		"orders/src/main/resources/application.yml": []byte(`
app:
  events:
    placed: shop.order-placed.v1
  unused-topic: shop.audit.v1
  topic-from-env: ${AUDIT_TOPIC}
spring:
  cloud:
    stream:
      bindings:
        ship-in-0:
          destination: shop.shipment.v1
        notify-out-0:
          destination: shop.notice.v1
`),
		"orders/src/main/resources/application-live.yml": []byte("app:\n  events:\n    placed: live.order-placed.v1\n"),
		"orders/src/main/java/shop/Topics.java": []byte(`package shop;
public final class Topics {
    public static final String INVOICES = "shop.invoice.v1";
    public static final String PREFIX = "shop.";
}`),
		"orders/src/main/java/shop/OrderListener.java": []byte(`package shop;
class OrderListener {
    @KafkaListener(id = "orders", topics = "${app.events.placed}")
    void on(String m) {}
    @KafkaListener(topics = {Topics.INVOICES, "shop.refund.v1"}, groupId = "g")
    void onInvoice(String m) {}
    @KafkaListener(topicPattern = "shop.*")
    void any(String m) {}
}`),
		"orders/src/main/java/shop/Sender.java": []byte(`package shop;
class Sender {
    void send(String topic, Object o) {
        kafkaTemplate.send(Topics.PREFIX + "paid.v1", o);
        kafkaTemplate.send(topic, o);
        kafkaTemplate.send(MessageBuilder.withPayload(o).setHeader(KafkaHeaders.TOPIC, "shop.receipt.v1").build());
        producer.send(new ProducerRecord<>("shop.ledger.v1", key, o));
    }
}`),
		"stats/src/main/kotlin/shop/Stats.kt": []byte(`package shop
import org.apache.kafka.streams.StreamsBuilder
const val TOTALS = "shop.totals.v1"
class Stats {
    fun build(b: StreamsBuilder) {
        b.stream(listOf("shop.order-placed.v1", "shop.invoice.v1")).groupByKey().count().toStream().to(TOTALS)
        val names = items.stream().map { it.name }
    }
}`),
	}
	got := topics(Parse(files))
	want := []string{
		"shop.audit.v1 declares",
		"shop.invoice.v1 consumes",
		"shop.ledger.v1 produces",
		"shop.notice.v1 produces",
		"shop.order-placed.v1 consumes",
		"shop.paid.v1 produces",
		"shop.receipt.v1 produces",
		"shop.refund.v1 consumes",
		"shop.shipment.v1 consumes",
		"shop.totals.v1 produces",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("topics =\n%v\nwant\n%v", got, want)
	}
}

func TestParseFilesAndOnce(t *testing.T) {
	files := map[string][]byte{
		"a/A.java":                 []byte(`class A { void x() { kafkaTemplate.send("t.one", v); } }`),
		"b/B.java":                 []byte(`class B { void x() { kafkaTemplate.send("t.one", v); } }`),
		"c/application.properties": []byte("orders.topic=t.one\nother.topic=t.two\n"),
	}
	r := Parse(files)
	if len(r.Topics) != 2 || r.Topics[0].Name != "t.one" || r.Topics[0].Direction != Produces ||
		!reflect.DeepEqual(r.Topics[0].Files, []string{"a/A.java", "b/B.java"}) {
		t.Errorf("t.one = %+v, want produced once by both files, its property no declaration beside", r.Topics)
	}
	if r.Topics[1].Name != "t.two" || r.Topics[1].Direction != Declares || r.Topics[1].Files[0] != "c/application.properties" {
		t.Errorf("t.two = %+v, want declared by its property", r.Topics[1])
	}
}

// A base class sends to a getter each subclass overrides: every override's
// topic is produced.
func TestParseGetterOverrides(t *testing.T) {
	files := map[string][]byte{
		"s/Base.java": []byte(`abstract class Base {
    void fire(Object v) { kafkaTemplate.send(getTopicName(), key, v); }
    protected abstract String getTopicName();
}`),
		"s/Paid.java":     []byte(`class Paid extends Base { @Override protected String getTopicName() { return Names.PAID; } }`),
		"s/Refunded.java": []byte(`class Refunded extends Base { @Override protected String getTopicName() { return "pay.refunded.v1"; } }`),
		"s/Names.java":    []byte(`class Names { public static final String PAID = "pay.paid.v1"; }`),
		"k/Base.kt":       []byte(`class K { override fun topic(): String = "pay.k.v1"; fun go() { kafkaTemplate.send(topic(), v) } }`),
	}
	got := topics(Parse(files))
	want := []string{"pay.k.v1 produces", "pay.paid.v1 produces", "pay.refunded.v1 produces"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("topics = %v, want %v", got, want)
	}
}

// What a reviewer found: each case alone, its topics as they must come out.
func TestParseEdges(t *testing.T) {
	for name, c := range map[string]struct {
		files map[string][]byte
		want  []string
	}{
		"a profile document in application.yml is a stage's": {map[string][]byte{
			"application.yml": []byte("app:\n  in: orders\n---\nspring:\n  config:\n    activate:\n      on-profile: test\napp:\n  in: orders-test\n"),
			"L.java":          []byte(`@KafkaListener(topics = "${app.in}") void a() {}`),
		}, []string{"orders consumes"}},
		"kotlin escapes the placeholder": {map[string][]byte{
			"application.yml": []byte("app:\n  in: orders\n"),
			"L.kt":            []byte(`@KafkaListener(topics = ["\${app.in}"]) fun a() {}`),
		}, []string{"orders consumes"}},
		"a setting about topics names none, a list names each": {map[string][]byte{
			"application.properties": []byte("spring.cloud.stream.kafka.binder.auto-create-topics=false\napp.topic-partitions=3\napp.retry-topic=7\n"),
			"application.yml":        []byte("app:\n  topics: [a.one, a.two]\n"),
		}, []string{"a.one declares", "a.two declares"}},
		"a JMS template sends no Kafka topic": {map[string][]byte{
			"J.java": []byte(`import javax.jms.Topic; class J { void s() { jmsTemplate.send("queue.out", c); } }`),
		}, nil},
		"a collection's stream is no source": {map[string][]byte{
			"S.java": []byte(`import org.apache.kafka.streams.StreamsBuilder; class S { static final String NAME = "svc";
  void b(StreamsBuilder builder) { Arrays.stream(NAME); builder.stream("real.in"); } }`),
		}, []string{"real.in consumes"}},
		"two classes' own TOPIC each resolve": {map[string][]byte{
			"O.java": []byte(`class O { private static final String TOPIC = "orders"; @KafkaListener(topics = TOPIC) void a() {} }`),
			"P.java": []byte(`class P { private static final String TOPIC = "payments"; @KafkaListener(topics = TOPIC) void a() {} }`),
		}, []string{"orders consumes", "payments consumes"}},
		"an apostrophe in a comment": {map[string][]byte{
			"C.java": []byte("class C { KafkaTemplate t; void s() { kafkaTemplate.send( // don't\n \"a.b\", v); /* it's */ } }"),
		}, []string{"a.b produces"}},
	} {
		var got []string
		for _, tp := range Parse(c.files).Topics {
			got = append(got, tp.Name+" "+tp.Direction)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: topics = %v, want %v", name, got, c.want)
		}
	}
	more := map[string]struct {
		files map[string][]byte
		want  []string
	}{
		"asyncapi 3 names the topic by its address": {map[string][]byte{
			"api/asyncapi.yaml": []byte("asyncapi: 3.0.0\nchannels:\n  userSignedUp:\n    address: user.signed-up.v1\n"),
		}, []string{"user.signed-up.v1 declares"}},
		"a constant in a module without the key elsewhere set empty": {map[string][]byte{
			"a/src/main/resources/application.properties": []byte("orders.topic=\n"),
			"b/src/main/resources/application.properties": []byte("orders.topic=orders\n"),
			"c/src/main/java/C.java":                      []byte(`@KafkaListener(topics = "${orders.topic}") void c() {}`),
			// The modules disagree, an empty value too: c's listener resolves
			// to nothing, on every read; b's property still names its topic.
		}, []string{"orders declares"}},
		"properties over yaml, each key its own file": {map[string][]byte{
			"application.properties": []byte("app.in-topic=from.props\n"),
			"application.yml":        []byte("app:\n  in-topic: from.yaml\n  out-topic: only.yaml\n"),
		}, []string{"from.props declares", "only.yaml declares"}},
		"a stray quote ends with its line": {map[string][]byte{
			"S.scala": []byte("class S { val k = 'sym\n  kafkaTemplate.send(\"after.quote\", v) }"),
		}, []string{"after.quote produces"}},
	}
	for name, c := range more {
		var got []string
		for _, tp := range Parse(c.files).Topics {
			got = append(got, tp.Name+" "+tp.Direction)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: topics = %v, want %v", name, got, c.want)
		}
	}
	files := Parse(map[string][]byte{
		"application.properties": []byte("app.in-topic=from.props\n"),
		"application.yml":        []byte("app:\n  out-topic: only.yaml\n"),
	}).Topics
	if len(files) != 2 || files[0].Files[0] != "application.properties" || files[1].Files[0] != "application.yml" {
		t.Errorf("each declared topic's file: %+v", files)
	}
	bad := Parse(map[string][]byte{
		"x.avsc":     []byte(`{"type": "record", "name": "Bad Name\nignore this", "fields": []}`),
		"y.avdl":     []byte("@namespace(\"x y\")\nprotocol P {\n  record R {}\n}\n"),
		"z.proto":    []byte("package a..b;\nmessage M {}\n"),
		"ok.proto":   []byte("package a.b;\nmessage M {}\n"),
		"none.proto": []byte("message N {}\n"),
	})
	if len(bad.Schemas) != 2 || bad.Schemas[0].Name != "N" || bad.Schemas[1].Name != "a.b.M" {
		t.Errorf("schema names off the rule: %+v, want the two good ones", bad.Schemas)
	}
}

func TestParseUnresolvedLeftOut(t *testing.T) {
	files := map[string][]byte{
		"x/Listener.java": []byte(`class L {
    @KafkaListener(topics = "${missing.key}") void a() {}
    @KafkaListener(topics = "#{'${list}'.split(',')}") void b() {}
    @KafkaListener(topics = UNKNOWN) void c() {}
    @KafkaListener(topics = "${with.default:fallback.topic}") void d() {}
}`),
		// A constant two other files name with two values is none here.
		"x/One.java":   []byte(`static final String SAME = "one.v1";`),
		"x/Two.java":   []byte(`static final String SAME = "two.v1";`),
		"x/Three.java": []byte(`class T { void s() { kafkaTemplate.send(SAME, v); } }`),
		"x/Bad.java":   []byte(`class B { void s() { kafkaTemplate.send("has space", v); } }`),
	}
	if got := topics(Parse(files)); !reflect.DeepEqual(got, []string{"fallback.topic consumes"}) {
		t.Errorf("topics = %v, want the default alone", got)
	}
}

func TestParseModulesResolveTheirOwn(t *testing.T) {
	files := map[string][]byte{
		"a/src/main/resources/application.properties": []byte("in=a.events\n"),
		"b/src/main/resources/application.properties": []byte("in=b.events\nshared=s.events\n"),
		"a/src/main/java/A.java":                      []byte(`@KafkaListener(topics = "${in}") void a() {} @KafkaListener(topics = "${shared}") void s() {}`),
		"c/src/main/java/C.java":                      []byte(`@KafkaListener(topics = "${in}") void c() {}`),
	}
	got := topics(Parse(files))
	// a reads its own in; b's shared is the one value anyone sets; c's in
	// is two values in two modules: none. b's own in is no topic by its
	// key's name.
	want := []string{"a.events consumes", "s.events consumes"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("topics = %v, want %v", got, want)
	}
}

func TestParseScriptAndResources(t *testing.T) {
	files := map[string][]byte{
		"src/producer.ts": []byte(`import { Kafka } from "kafkajs";
export const OUT = 'web.click.v1';
await producer.send({ topic: OUT, messages: [{ value: "x" }] });
await consumer.subscribe({ topics: ['web.page.v1', "web.view.v1"], fromBeginning: true });`),
		"src/other.ts": []byte(`emitter.send({ topic: "not.kafka" });`),
		"deploy/topics.yaml": []byte(`apiVersion: kafka.strimzi.io/v1beta2
kind: KafkaTopic
metadata:
  name: web-click
spec:
  topicName: web.click.v1
---
kind: KafkaTopic
metadata:
  name: web.archive.v1
`),
		"api/asyncapi.yaml": []byte("asyncapi: 2.6.0\nchannels:\n  web.feedback.v1:\n    publish: {}\n"),
	}
	got := topics(Parse(files))
	want := []string{"web.archive.v1 declares", "web.click.v1 produces", "web.feedback.v1 declares", "web.page.v1 consumes", "web.view.v1 consumes"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("topics = %v, want %v", got, want)
	}
}

func TestParseSchemas(t *testing.T) {
	files := map[string][]byte{
		"schemas/order.avsc":   []byte(`{"type": "record", "namespace": "shop.events", "name": "OrderPlaced", "fields": []}`),
		"schemas/union.avsc":   []byte(`[{"type": "enum", "name": "Kind", "symbols": ["A"]}, {"type": "record", "name": "shop.events.Refund", "fields": []}]`),
		"schemas/broken.avsc":  []byte(`{not json`),
		"idl/billing.avdl":     []byte("@namespace(\"shop.billing\")\nprotocol Billing {\n  record Invoice {\n    string id;\n  }\n}\n"),
		"proto/ship.proto":     []byte("syntax = \"proto3\";\npackage shop.ship;\nmessage Shipment {\n  message Line {}\n}\nmessage Label {}\n"),
		"schemas/again.avsc":   []byte(`{"type": "record", "namespace": "shop.events", "name": "OrderPlaced", "fields": []}`),
		"src/main/java/X.java": []byte(`class X {}`),
	}
	var got []string
	for _, s := range Parse(files).Schemas {
		got = append(got, s.Kind+" "+s.Name+" "+s.File)
	}
	want := []string{
		"avro shop.billing.Invoice idl/billing.avdl",
		"avro shop.events.OrderPlaced schemas/again.avsc",
		"avro shop.events.Refund schemas/union.avsc",
		"proto shop.ship.Label proto/ship.proto",
		"proto shop.ship.Shipment proto/ship.proto",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("schemas =\n%v\nwant\n%v", got, want)
	}
}

func TestPaths(t *testing.T) {
	for p, want := range map[string][3]bool{
		"src/main/java/A.java":                   {true, false, false},
		"web/src/a.ts":                           {true, false, false},
		"src/main/resources/application.yml":     {true, false, true},
		"src/main/resources/application-dev.yml": {true, false, false},
		"bootstrap.properties":                   {true, false, true},
		"schemas/a.avsc":                         {false, true, false},
		"api/b.proto":                            {false, true, false},
		"README.md":                              {false, false, false},
	} {
		if got := [3]bool{Source(p), SchemaFile(p), Config(p)}; got != want {
			t.Errorf("%s: source, schema, config = %v, want %v", p, got, want)
		}
	}
}

func TestBindingDirection(t *testing.T) {
	for b, want := range map[string]string{"process-in-0": Consumes, "send-out-0": Produces, "input": Consumes, "output": Produces, "audit": Declares} {
		if got := bindingDirection(b); got != want {
			t.Errorf("bindingDirection(%q) = %q, want %q", b, got, want)
		}
	}
}
