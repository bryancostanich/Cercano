import assert from "node:assert/strict";
import { access, readFile, stat } from "node:fs/promises";
import test from "node:test";

test("exports the landing page beneath the Cercano project path", async () => {
  const html = await readFile(new URL("../out/index.html", import.meta.url), "utf8");

  assert.match(html, /Install in one line/);
  assert.match(html, /\/Cercano\/_next\/static\//);
  assert.match(html, /\/Cercano\/videos\/sessions\.mp4/);
  assert.match(html, /https:\/\/bryancostanich\.github\.io\/Cercano/);
  assert.doesNotMatch(html, /cercano-ai-agent\.b-c119\.chatgpt\.site/);
});

test("exports every public demonstration and disables Jekyll processing", async () => {
  await access(new URL("../out/.nojekyll", import.meta.url));

  for (const name of ["delegation", "metrics", "terminal-ui", "sessions"]) {
    const [video, poster] = await Promise.all([
      stat(new URL(`../out/videos/${name}.mp4`, import.meta.url)),
      stat(new URL(`../out/videos/${name}.jpg`, import.meta.url)),
    ]);
    assert.ok(video.size > 100_000, `${name}.mp4 should be present in the Pages artifact`);
    assert.ok(poster.size > 20_000, `${name}.jpg should be present in the Pages artifact`);
  }
});
