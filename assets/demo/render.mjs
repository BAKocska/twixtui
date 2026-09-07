// Run with Bun. The browser and encoder are export tools, not game dependencies.
import puppeteer from "puppeteer-core";
import { access, rename, rm } from "node:fs/promises";
import { spawn } from "node:child_process";
import { once } from "node:events";
import path from "node:path";

const source = import.meta.dir;
const output = path.resolve(process.argv[2] || path.join(source, ".."));
const executablePath = process.env.CHROME || [
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  Bun.which("chromium"), Bun.which("chromium-browser"), Bun.which("google-chrome"),
].find(candidate => candidate && Bun.file(candidate).size > 0);
if (!executablePath) throw new Error("Set CHROME to a Chrome or Chromium executable.");
await access(output);
const encoderPath = process.env.FFMPEG || Bun.which("ffmpeg");
if (!encoderPath) throw new Error("Install ffmpeg or set FFMPEG to its executable.");
const fps = 30;
const selector = "[data-om-exportable-video-with-duration-secs]";
const allowed = new Set(["index.html", "support.js", "animations-v3.jsx", "demo-video.jsx"]);
const server = Bun.serve({
  hostname: "127.0.0.1", port: 0,
  fetch(request) {
    const name = new URL(request.url).pathname.slice(1) || "index.html";
    if (!allowed.has(name)) return new Response("Not found", { status: 404 });
    return new Response(Bun.file(path.join(source, name)), {
      headers: { "Cache-Control": "no-store" },
    });
  },
});
let browser;
let encoder;
const movie = path.join(output, "twixtui-demo.mp4");
const poster = path.join(output, "demo-poster.png");
const temporaryMovie = path.join(output, `.twixtui-demo-${process.pid}.mp4`);
const temporaryPoster = path.join(output, `.demo-poster-${process.pid}.png`);
try {
  browser = await puppeteer.launch({
    executablePath, headless: true,
    defaultViewport: { width: 1280, height: 764, deviceScaleFactor: 1 },
  });
  const page = await browser.newPage();
  const failures = [];
  page.on("pageerror", error => failures.push(error.message));
  await page.goto(`http://127.0.0.1:${server.port}/`, { waitUntil: "networkidle0" });
  await page.waitForSelector(`${selector}[data-om-sync-seek="true"][data-om-fonts-inlined="true"]`, { timeout: 60000 });
  await page.evaluate(async () => {
    const regular = await document.fonts.load('400 13px "JetBrains Mono"');
    const bold = await document.fonts.load('700 13px "JetBrains Mono"');
    await document.fonts.ready;
    if (!regular.length || !bold.length ||
        [...regular, ...bold].some(face => face.status !== "loaded")) {
      throw new Error("The regular and bold demo fonts must load before export.");
    }
  });
  const stage = await page.$(selector);
  const duration = await stage.evaluate(el => Number(el.getAttribute("data-om-exportable-video-with-duration-secs")));
  if (!(duration > 0 && duration < 300)) throw new Error(`Invalid duration: ${duration}`);
  const clip = await stage.boundingBox();
  if (!clip || Math.abs(clip.width - 1280) > 0.01 || Math.abs(clip.height - 720) > 0.01) {
    throw new Error(`Unexpected stage dimensions: ${JSON.stringify(clip)}`);
  }
  const seek = async time => {
    await stage.evaluate((el, t) => {
      el.dispatchEvent(new CustomEvent("data-om-seek-to-time-frame", {
        detail: { time: t, sync: true, playing: false },
      }));
    }, time);
    if (failures.length) throw new Error(failures.join("\n"));
  };
  await seek(10);
  await page.screenshot({ path: temporaryPoster, type: "png", clip });
  let encoderError = "";
  encoder = spawn(encoderPath, [
    "-hide_banner", "-loglevel", "error", "-y",
    "-f", "image2pipe", "-framerate", String(fps), "-c:v", "png", "-i", "pipe:0",
    "-an", "-c:v", "libx264", "-preset", "slow", "-crf", "18", "-threads", "2",
    "-pix_fmt", "yuv420p", "-movflags", "+faststart", "-map_metadata", "-1",
    temporaryMovie,
  ], { stdio: ["pipe", "ignore", "pipe"] });
  encoder.stderr.on("data", data => { encoderError += data.toString(); });
  encoder.stdin.on("error", () => {}); // Report the encoder's diagnostic below.
  const finished = new Promise(resolve => {
    encoder.once("error", error => resolve({ error }));
    encoder.once("exit", (code, signal) => resolve({ code, signal }));
  });
  const frames = Math.ceil(duration * fps);
  console.log(`Rendering ${frames} frames: ${duration}s, 1280x720, ${fps}fps`);
  for (let frame = 0; frame < frames; frame++) {
    await seek(frame / fps);
    const image = await page.screenshot({ type: "png", clip });
    if (encoder.exitCode !== null || encoder.stdin.destroyed) throw new Error(encoderError || "Encoder stopped.");
    if (!encoder.stdin.write(image)) await once(encoder.stdin, "drain");
    if (frame % 300 === 0) console.log(`Frame ${frame}/${frames}`);
  }
  encoder.stdin.end();
  const result = await finished;
  if (result.error || result.code !== 0) throw new Error(encoderError || String(result.error || result.signal));
  await rename(temporaryMovie, movie);
  await rename(temporaryPoster, poster);
  console.log(`Wrote ${movie} (${(Bun.file(movie).size / 1024 / 1024).toFixed(2)} MiB)`);
  console.log(`Wrote ${poster}`);
} finally {
  if (encoder && encoder.exitCode === null) encoder.kill("SIGTERM");
  if (browser) await browser.close();
  server.stop(true);
  await rm(temporaryMovie, { force: true });
  await rm(temporaryPoster, { force: true });
}
