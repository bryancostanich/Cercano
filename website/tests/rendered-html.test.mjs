import assert from "node:assert/strict";
import { readFile, stat } from "node:fs/promises";
import test from "node:test";

async function render() {
  const workerUrl = new URL("../dist/server/index.js", import.meta.url);
  workerUrl.searchParams.set("test", `${process.pid}-${Date.now()}`);
  const { default: worker } = await import(workerUrl.href);
  return worker.fetch(new Request("http://localhost/", { headers: { accept: "text/html" } }), {
    ASSETS: { fetch: async () => new Response("Not found", { status: 404 }) },
  }, { waitUntil() {}, passThroughOnException() {} });
}

test("renders the Cercano landing page", async () => {
  const response = await render();
  assert.equal(response.status, 200);
  assert.match(response.headers.get("content-type") ?? "", /^text\/html\b/i);
  const html = await response.text();
  assert.match(html, /Use context and compute deliberately/);
  assert.match(html, /Frontier \+ open/);
  assert.match(html, /An agent designed around the work/);
  assert.match(html, /Delegate work\. Keep your focus/);
  assert.match(html, /Know where the tokens go/);
  assert.match(html, /A terminal that stays readable/);
  assert.match(html, /Resume the session, not the setup/);
  assert.match(html, /One agent\. More than one client/);
  assert.match(html, /100% free and open source under Apache 2\.0/);
  for (const video of ["delegation", "metrics", "terminal-ui", "sessions"]) {
    assert.match(html, new RegExp(`/videos/${video}\\.mp4`));
    assert.match(html, new RegExp(`/videos/${video}\\.jpg`));
  }
  assert.match(html, /Install Cercano/);
  assert.match(html, /Install in one line/);
  assert.match(html, /brew install cercano-ai\/tap\/cercano/);
  assert.match(html, /Homebrew/);
  assert.match(html, /Linux · coming soon/);
  assert.match(html, /Windows · coming soon/);
  assert.match(html, /href="https:\/\/github\.com\/cercano-ai\/Cercano"[^>]*>Cercano - 100% Free \+ Open<\/a>/);
  assert.match(html, /Switch to (daylight|night) theme/);
  assert.doesNotMatch(html, /codex-preview|Building your site|react-loading-skeleton/);
});

test("keeps the TUI palettes and persistent theme behavior", async () => {
  const [css, page, layout] = await Promise.all([
    readFile(new URL("../app/globals.css", import.meta.url), "utf8"),
    readFile(new URL("../app/page.tsx", import.meta.url), "utf8"),
    readFile(new URL("../app/layout.tsx", import.meta.url), "utf8"),
  ]);

  for (const color of ["#fbf3e0", "#3b3020", "#1e7a3c", "#1763a0", "#1a1a1a", "#ea8212", "#bdf000", "#00c8e8"]) {
    assert.match(css, new RegExp(color, "i"));
  }
  assert.match(css, /--font-mono:\s*var\(--font-plex-mono\)/);
  assert.match(css, /feature-story--visual:nth-child\(odd\)[^{]*\{[^}]*1\.34fr[^}]*\.66fr/s);
  assert.match(page, /localStorage\.setItem\("cercano-theme"/);
  assert.match(page, /navigator\.clipboard\.writeText\("brew install cercano-ai\/tap\/cercano"\)/);
  assert.match(layout, /prefers-color-scheme:\s*dark/);
  assert.match(layout, /IBM_Plex_Mono/);
});

test("ships all four README demonstrations and their poster frames", async () => {
  for (const name of ["delegation", "metrics", "terminal-ui", "sessions"]) {
    const [video, poster] = await Promise.all([
      stat(new URL(`../public/videos/${name}.mp4`, import.meta.url)),
      stat(new URL(`../public/videos/${name}.jpg`, import.meta.url)),
    ]);
    assert.ok(video.size > 100_000, `${name}.mp4 should contain the demonstration`);
    assert.ok(poster.size > 20_000, `${name}.jpg should contain a useful poster frame`);
  }
});
