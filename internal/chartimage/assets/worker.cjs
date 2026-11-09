'use strict';
// Local-only entry point. Request JSON contains market data, never paths,
// credentials, HTML, JavaScript, URLs, or Telegram configuration.
const fs = require('node:fs');
const path = require('node:path');
const MAX_INPUT = 2 * 1024 * 1024;
const MAX_IMAGE = 8 * 1024 * 1024;

async function main() {
  const [runtimeDirectory, browserExecutable] = process.argv.slice(2);
  if (!path.isAbsolute(runtimeDirectory || '') || !path.isAbsolute(browserExecutable || '')) throw Error('Invalid runtime');
  const moduleDirectory = path.join(runtimeDirectory, 'node_modules', 'playwright-core');
  if (JSON.parse(fs.readFileSync(path.join(moduleDirectory, 'package.json'), 'utf8')).version !== '1.64.1') throw Error('Runtime version mismatch');
  const chunks = [];
  let length = 0;
  for await (const chunk of process.stdin) {
    length += chunk.length;
    if (length > MAX_INPUT) throw Error('Input limit');
    chunks.push(chunk);
  }
  const request = JSON.parse(Buffer.concat(chunks).toString('utf8'));
  const {chromium} = require(moduleDirectory);
  const browser = await chromium.launch({executablePath:browserExecutable,headless:true,args:['--disable-background-networking','--disable-component-update','--disable-domain-reliability','--disable-sync','--metrics-recording-only','--no-first-run']});
  try {
    const context = await browser.newContext({viewport:{width:1440,height:1000},deviceScaleFactor:1,timezoneId:'UTC',locale:'en-US',acceptDownloads:false,serviceWorkers:'block'});
    await context.route('**/*', route => route.abort());
    await context.routeWebSocket('**/*', route => route.close());
    const page = await context.newPage();
    const pageErrors = [];
    page.on('pageerror', () => pageErrors.push('Chart error'));
    await page.setContent('<!doctype html><html><head><meta charset="UTF-8"><meta http-equiv="Content-Security-Policy" content="default-src \'none\'; script-src \'unsafe-inline\'; style-src \'unsafe-inline\'; img-src data:; font-src \'none\'; connect-src \'none\'; frame-src \'none\'; object-src \'none\'; base-uri \'none\'"></head><body><div id="root"></div></body></html>');
    await page.addStyleTag({path:path.join(__dirname,'chart.css')});
    await page.addScriptTag({path:path.join(__dirname,'chart.js')});
    await page.evaluate(request => window.__renderSignalChart(request), request);
    await page.waitForFunction(() => {
      const host = document.querySelector('[data-testid="analysis-chart"]');
      return !!document.querySelector('.analysis-error') || (!!host?.dataset.initialBarSpacing && !!host.querySelector('canvas'));
    },undefined,{timeout:12000});
    await page.evaluate(async () => { await document.fonts.ready; await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))); });
    if (pageErrors.length || await page.locator('.analysis-error').count()) throw Error('Chart error');
    const chart = page.locator('.signal-chart');
    const bounds = await chart.boundingBox();
    if (!bounds || Math.ceil(bounds.width) + Math.ceil(bounds.height) > 10000 || bounds.height > 8500) throw Error('Image dimensions');
    const image = await chart.screenshot({type:'png',animations:'disabled',timeout:12000});
    if (image.length > MAX_IMAGE) throw Error('Image limit');
    process.stdout.write(image);
  } finally {
    await browser.close();
  }
}

main().catch(() => { process.stderr.write('Signal chart rendering failed\n'); process.exitCode = 1; });
