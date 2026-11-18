"use strict";

// Puts NeoExit (and, if asked, HMray Smart Routing) into a whole Xray
// config pasted from 3x-ui, each piece where Xray needs it:
//
// - the "neoexit" outbound goes last: Xray sends unmatched traffic to the
//   FIRST outbound, so the user's default stays their default;
// - the rules go right after the leading api and block rules (3x-ui's
//   "api", "geoip:private → blocked", "bittorrent → blocked"), so those
//   still win and nothing of the user's catches AI traffic first;
// - a domain list and an IP list are separate rules, because the
//   conditions inside one Xray rule must ALL match;
// - running it again replaces what it added before (rules carry a
//   "ruleTag" starting with "neoexit"), nothing is added twice.
//
// It runs in the browser only and keeps the order of the user's keys.
const XRAY = (() => {
  const site = (n) => "ext:hmray.dat:" + n;
  const ip = (n) => "ext:hmray-geoip.dat:" + n;

  // Categories of hmray.dat / hmray-geoip.dat (all checked against the
  // files), in the order their rules are placed. def: the suggested target.
  const CATS = [
    { id: "block", name: "تبلیغات، بدافزار، فیشینگ و تورنت", def: "@block",
      rules: [{ domain: [site("ads"), site("malware"), site("phishing")] }, { protocol: ["bittorrent"] }] },
    { id: "iran", name: "سایت‌ها و IPهای ایران", def: "@direct",
      rules: [{ domain: [site("iran"), "regexp:\\.ir$"] },
        { ip: [ip("iran"), ip("arvancloud"), ip("derakcloud"), ip("iranserver"), ip("parspack")] }] },
    { id: "sanctioned", name: "سایت‌هایی که ایران را تحریم کرده‌اند", def: "",
      rules: [{ domain: [site("sanctioned")] }] },
    { id: "gaming", name: "بازی (Steam، Riot، Epic، کنسول‌ها…)", def: "",
      rules: [{ domain: [site("gaming")] }, { ip: [ip("game-riot"), ip("game-steam"), ip("game-blizzard")] }] },
    { id: "streaming", name: "YouTube و استریم (Netflix، Spotify، Twitch…)", def: "",
      rules: [{ domain: [site("youtube"), site("streaming"), site("netflix")] }, { ip: [ip("netflix")] }] },
    { id: "messaging", name: "پیام‌رسان‌ها (تلگرام، واتساپ، دیسکورد…)", def: "",
      rules: [{ domain: [site("messaging"), site("telegram"), site("discord")] }, { ip: [ip("telegram")] }] },
    { id: "developer", name: "برنامه‌نویسی (GitHub، npm، Docker…)", def: "",
      rules: [{ domain: [site("github"), site("developer")] }, { ip: [ip("github")] }] },
    { id: "adult", name: "بزرگسالان", def: "",
      rules: [{ domain: [site("adult")] }] },
  ];
  const AI_SMART = [site("ai"), site("openai")];

  // JSON with // and /* */ comments and trailing commas, as people paste it.
  function clean(text) {
    let out = "", i = 0, str = false;
    while (i < text.length) {
      const c = text[i], n = text[i + 1];
      if (str) {
        out += c;
        if (c === "\\") { out += n || ""; i += 2; continue; }
        if (c === '"') str = false;
        i++;
      } else if (c === '"') { str = true; out += c; i++; }
      else if (c === "/" && n === "/") { while (i < text.length && text[i] !== "\n") i++; }
      else if (c === "/" && n === "*") { i = text.indexOf("*/", i + 2); i = i < 0 ? text.length : i + 2; }
      else { out += c; i++; }
    }
    return out.replace(/,(\s*[}\]])/g, "$1");
  }

  function parse(text) {
    if (!text.trim()) throw new Error("تنظیمات را اینجا بچسبانید.");
    let cfg;
    try { cfg = JSON.parse(clean(text.replace(/^﻿/, ""))); }
    catch (e) { throw new Error("این JSON درست نیست: " + e.message); }
    if (!cfg || typeof cfg !== "object" || Array.isArray(cfg)) throw new Error("این تنظیمات Xray نیست.");
    if (!Array.isArray(cfg.outbounds) || !cfg.outbounds.length)
      throw new Error("بخش outbounds در این تنظیمات نیست؛ کل تنظیمات Xray را کپی کنید، نه فقط یک قسمت.");
    return cfg;
  }

  // The tags a category can be sent to, from the pasted config.
  function tags(cfg) {
    return (cfg.outbounds || []).filter((o) => o && o.tag && o.tag !== "neoexit").map((o) => ({ tag: o.tag, protocol: o.protocol || "" }));
  }

  const isOurs = (r) => r && ((typeof r.ruleTag === "string" && r.ruleTag.startsWith("neoexit")) || r.outboundTag === "neoexit");
  const list = (v) => (Array.isArray(v) ? v : v == null ? [] : [v]);

  // opts: { outbound, rule (NeoExit's AI rule), smart, cats: { id: target } }
  // where a target is "" (leave alone), "@block", "@direct" or a tag.
  function merge(text, opts) {
    const cfg = parse(text);
    const notes = [];
    const obs = cfg.outbounds;

    const before = obs.length;
    for (let i = obs.length - 1; i >= 0; i--) if (obs[i] && obs[i].tag === "neoexit") obs.splice(i, 1);
    if (!obs.length) throw new Error("به‌جز neoexit خروجی دیگری در تنظیمات نیست.");
    obs.push(opts.outbound);
    notes.push(before > obs.length - 1 ? "خروجی neoexit با نسخه‌ی تازه عوض شد (آخر فهرست)." : "خروجی neoexit به آخر فهرست خروجی‌ها اضافه شد؛ خروجی پیش‌فرض شما همان اولی می‌ماند.");

    const want = Object.entries(opts.smart ? opts.cats || {} : {}).filter(([, t]) => t);
    const byProto = (p) => obs.find((o) => o.protocol === p && o.tag === (p === "freedom" ? "direct" : "blocked")) || obs.find((o) => o.protocol === p && o.tag && o.tag !== "neoexit");
    const special = {};
    for (const [p, tag, word] of [["blackhole", "blocked", "@block"], ["freedom", "direct", "@direct"]]) {
      if (!want.some(([, t]) => t === word)) continue;
      let o = byProto(p);
      if (!o) {
        let t = tag;
        for (let n = 2; obs.some((x) => x.tag === t); n++) t = tag + n;
        o = { tag: t, protocol: p, settings: {} };
        obs.splice(obs.length - 1, 0, o); // before neoexit, never first
        notes.push(`خروجی «${t}» (${p}) نبود و ساخته شد.`);
      }
      special[word] = o.tag;
    }
    const resolve = (t) => special[t] || t;
    for (const [, t] of want) {
      if (!t.startsWith("@") && !obs.some((o) => o.tag === t)) throw new Error(`خروجی «${t}» در این تنظیمات نیست.`);
    }

    // NeoExit's rules: block first, then AI, then the rest.
    const ours = [];
    const add = (id, tag, cond) => ours.push(Object.assign({ type: "field", ruleTag: "neoexit-" + id, outboundTag: tag }, cond));
    const cat = (id) => CATS.find((c) => c.id === id);
    const place = (id) => {
      const t = (opts.cats || {})[id];
      if (!opts.smart || !t) return;
      cat(id).rules.forEach((r, i) => add(id + (i ? "-" + (i + 1) : ""), resolve(t), JSON.parse(JSON.stringify(r))));
    };
    place("block");
    const aiDomains = [...list(opts.rule && opts.rule.domain), ...(opts.smart ? AI_SMART : [])];
    if (aiDomains.length) add("ai", "neoexit", { domain: aiDomains });
    for (const c of CATS) if (c.id !== "block") place(c.id);

    cfg.routing = cfg.routing && typeof cfg.routing === "object" ? cfg.routing : {};
    const rules = Array.isArray(cfg.routing.rules) ? cfg.routing.rules : [];
    const old = rules.filter(isOurs).length;
    const kept = rules.filter((r) => !isOurs(r));
    const blockTags = new Set(obs.filter((o) => o.protocol === "blackhole").map((o) => o.tag));
    let at = 0;
    while (at < kept.length) {
      const r = kept[at] || {};
      const api = r.outboundTag === "api" || list(r.inboundTag).includes("api");
      if (!api && !blockTags.has(r.outboundTag)) break;
      at++;
    }
    kept.splice(at, 0, ...ours);
    cfg.routing.rules = kept;
    if (old) notes.push(`${old} قانونی که NeoExit قبلاً گذاشته بود برداشته شد و قوانین تازه جایش نشست.`);
    notes.push(`${ours.length} قانون ${at ? "بعد از قوانین api و مسدودسازی خودتان" : "در ابتدای قوانین"} گذاشته شد؛ قوانین دیگر شما دست نخورد.`);

    if (ours.some((r) => r.ip)) {
      const ds = cfg.routing.domainStrategy;
      if (!ds || ds === "AsIs") {
        cfg.routing.domainStrategy = "IPIfNonMatch";
        notes.push("domainStrategy از " + (ds || "خالی") + " به IPIfNonMatch عوض شد تا قوانین IP برای دامنه‌ها هم کار کنند.");
      }
    }
    const smartUsed = ours.some((r) => JSON.stringify(r).includes("ext:hmray"));
    return { text: JSON.stringify(cfg, null, 2), notes, smartUsed, count: ours.length };
  }

  return { CATS, clean, parse, tags, merge };
})();

if (typeof module !== "undefined") module.exports = XRAY;
