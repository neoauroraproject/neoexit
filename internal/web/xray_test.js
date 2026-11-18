// node internal/web/xray_test.js (go test runs it when node is there)
"use strict";
const assert = require("assert");
const X = require("./static/xray.js");

// 3x-ui's default Xray template, with a comment and a trailing comma.
const template = `{
  "log": { "access": "none", "dnsLog": false, "error": "", "loglevel": "warning", "maskAddress": "" },
  "api": { "tag": "api", "services": ["HandlerService", "LoggerService", "StatsService"] },
  "inbounds": [{ "tag": "api", "listen": "127.0.0.1", "port": 62789, "protocol": "dokodemo-door", "settings": { "address": "127.0.0.1" } }],
  // outbounds
  "outbounds": [
    { "tag": "direct", "protocol": "freedom", "settings": { "domainStrategy": "AsIs", "redirect": "", "noises": [] } },
    { "tag": "blocked", "protocol": "blackhole", "settings": {} },
    { "tag": "TR", "protocol": "vless", "settings": {} },
  ],
  "policy": {},
  "routing": {
    "domainStrategy": "AsIs",
    "rules": [
      { "type": "field", "inboundTag": ["api"], "outboundTag": "api" },
      { "type": "field", "outboundTag": "blocked", "ip": ["geoip:private"] },
      { "type": "field", "outboundTag": "blocked", "protocol": ["bittorrent"] },
      { "type": "field", "outboundTag": "TR", "domain": ["geosite:openai"] },
      { "type": "field", "outboundTag": "TR", "network": "tcp,udp" }
    ]
  },
  "stats": {}
}`;

const outbound = { tag: "neoexit", protocol: "socks", settings: { servers: [{ address: "127.0.0.1", port: 30000 }] } };
const rule = { type: "field", outboundTag: "neoexit", domain: ["domain:chatgpt.com", "domain:claude.ai"] };

// Plain: only the outbound and the AI rule.
let r = X.merge(template, { outbound, rule, smart: false, cats: { gaming: "TR" } });
let c = JSON.parse(r.text);
assert.deepStrictEqual(c.outbounds.map((o) => o.tag), ["direct", "blocked", "TR", "neoexit"], "neoexit goes last");
assert.deepStrictEqual(c.routing.rules.map((x) => x.ruleTag || x.outboundTag),
  ["api", "blocked", "blocked", "neoexit-ai", "TR", "TR"], "AI rule after api/block rules, before the user's");
assert.strictEqual(c.routing.domainStrategy, "AsIs", "no IP rules: strategy untouched");
assert.deepStrictEqual(Object.keys(c), ["log", "api", "inbounds", "outbounds", "policy", "routing", "stats"], "key order kept");

// Again, with smart routing: replaces, never doubles.
r = X.merge(r.text, { outbound, rule, smart: true, cats: { block: "@block", iran: "@direct", gaming: "TR", adult: "" } });
c = JSON.parse(r.text);
assert.strictEqual(c.outbounds.filter((o) => o.tag === "neoexit").length, 1);
assert.deepStrictEqual(c.routing.rules.map((x) => x.ruleTag || x.outboundTag), ["api", "blocked", "blocked",
  "neoexit-block", "neoexit-block-2", "neoexit-ai", "neoexit-iran", "neoexit-iran-2", "neoexit-gaming", "neoexit-gaming-2", "TR", "TR"]);
const by = (t) => c.routing.rules.find((x) => x.ruleTag === t);
assert.strictEqual(by("neoexit-block").outboundTag, "blocked", "@block uses the existing blackhole");
assert.strictEqual(by("neoexit-iran").outboundTag, "direct", "@direct uses the existing freedom");
assert.strictEqual(by("neoexit-gaming").outboundTag, "TR");
assert.ok(by("neoexit-ai").domain.includes("ext:hmray.dat:ai"));
assert.ok(!by("neoexit-iran-2").domain && by("neoexit-iran-2").ip, "domain and IP lists in separate rules");
assert.strictEqual(c.routing.domainStrategy, "IPIfNonMatch", "IP rules need IPIfNonMatch");
assert.ok(r.smartUsed);

// No blackhole/freedom outbound: made, but never as the first outbound.
const bare = JSON.stringify({ outbounds: [{ tag: "out", protocol: "vless" }] });
r = X.merge(bare, { outbound, rule, smart: true, cats: { block: "@block", iran: "@direct" } });
c = JSON.parse(r.text);
assert.deepStrictEqual(c.outbounds.map((o) => o.tag), ["out", "blocked", "direct", "neoexit"]);
assert.strictEqual(c.routing.rules[0].ruleTag, "neoexit-block");

// Errors people will hit.
assert.throws(() => X.merge("", { outbound, rule }), /بچسبانید/);
assert.throws(() => X.merge("{ nope", { outbound, rule }), /JSON/);
assert.throws(() => X.merge(JSON.stringify({ routing: {} }), { outbound, rule }), /outbounds/);
assert.throws(() => X.merge(bare, { outbound, rule, smart: true, cats: { gaming: "NL" } }), /NL/);

// Comments and "//" inside strings.
assert.strictEqual(JSON.parse(X.clean('{"u":"https://x/y", // c\n"a":1,}')).u, "https://x/y");

console.log("xray.js: all checks passed");
