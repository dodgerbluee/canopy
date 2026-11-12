// T34 e2e stub AI provider: speaks the real /v1/analyze wire contract so
// the worker exercises its full path (fetch → validate → project) without
// any external service. Output is built from the snapshot the server sent,
// so every evidenceId and citation it returns is one the validator will
// actually accept. STUB_DELAY_MS slows the reply so cancel-mid-run is
// testable. Never used outside the e2e harness.
import { createServer } from 'node:http';

const PORT = Number(process.env.STUB_PORT ?? 8098);
const DELAY_MS = Number(process.env.STUB_DELAY_MS ?? 0);

function synthResult(snap) {
  const evIds = [];
  if (snap.activities?.length) evIds.push(snap.activities[0].id);
  if (snap.photos?.length) evIds.push(snap.photos[0].id);
  const citations = snap.guidance?.length
    ? [
        {
          sourceId: snap.guidance[0].sourceId,
          sourceVersionId: snap.guidance[0].sourceVersionId,
          title: 'E2E care notes',
          publisher: 'E2E Owner Note',
          retrievedOn: '2026-11-01',
          region: '',
          license: '',
        },
      ]
    : [];
  return {
    visibleObservations: [
      { text: 'Recent care activity is on schedule.', evidenceIds: evIds },
      { text: 'A closer look at the leaf tips may help.', evidenceIds: [] },
    ],
    // qualified on purpose: an asserted causal claim would be rejected
    // output, which is exactly what the validator test suite covers.
    hypotheses: [
      {
        text: 'Irregular light may correlate with leaf condition.',
        confidence: 'MEDIUM',
        evidenceIds: evIds.slice(0, 1),
      },
    ],
    missingInformation: ['No humidity readings are recorded.'],
    followUpChecks: [
      'Check the leaf tips again in 7 days.',
      'Photograph the whole plant in 14 days.',
    ],
    citations,
  };
}

// Receipt structuring arrives on the same text port with a marker
// snapshot (receiptocr.go structure()): OCR text in, ReceiptResult out.
// The parse mirrors localParseReceipt so the capture journey sees the
// same values it saw when no text engine was configured.
function receiptResult(ocrText) {
  const totalRe =
    /^(?:total|amount due|balance due|total due|grand total)\D{0,3}?([0-9]{1,3}(?:,[0-9]{3})*\.[0-9]{2})(?:\s*[A-Za-z]{3})?\s*$/i;
  const lines = ocrText.split('\n');
  let total = '';
  for (let i = lines.length - 1; i >= 0; i--) {
    const m = totalRe.exec(lines[i].trim());
    if (m) {
      total = m[1];
      break;
    }
  }
  let totalMinor = '';
  if (total) {
    const [d, c] = total.replace(/,/g, '').split('.');
    if (d !== undefined && c !== undefined) totalMinor = String(Number(d) * 100 + Number(c));
  }
  let currency = '';
  const iso = /\b(USD|EUR|GBP|CAD|AUD|NZD|CHF|SEK|NOK|DKK|JPY|CNY|INR|BRL|MXN|SGD|HKD)\b/.exec(ocrText);
  if (iso) currency = iso[1];
  else {
    const syms = ocrText.match(/[$€£]/g) || [];
    if (syms.length && syms.every((s) => s === syms[0]))
      currency = { $: 'USD', '€': 'EUR', '£': 'GBP' }[syms[0]] ?? '';
  }
  let purchasedOn = '';
  const isoD = /\b(\d{4}-\d{2}-\d{2})\b/.exec(ocrText);
  if (isoD) purchasedOn = isoD[1];
  else {
    const us = /\b(\d{1,2})\/(\d{1,2})\/(\d{4})\b/.exec(ocrText);
    if (us) {
      const mo = Number(us[1]);
      const dy = Number(us[2]);
      if (mo >= 1 && mo <= 12 && dy >= 1 && dy <= 31)
        purchasedOn = `${us[3]}-${String(mo).padStart(2, '0')}-${String(dy).padStart(2, '0')}`;
    }
  }
  return {
    currency,
    lines: [],
    totalMinor,
    shippingMinor: '',
    taxMinor: '',
    purchasedOn,
    seller: '',
    needsReconciliation: true,
  };
}

const server = createServer((req, res) => {
  if (req.method !== 'POST' || !req.url.endsWith('/v1/analyze')) {
    res.writeHead(404).end();
    return;
  }
  let body = '';
  req.on('data', (c) => (body += c));
  req.on('end', async () => {
    let result;
    try {
      const msg = JSON.parse(body);
      // the adapter sends snapshot as a raw JSON object (json.RawMessage)
      const snap = typeof msg.snapshot === 'string' ? JSON.parse(msg.snapshot) : msg.snapshot;
      console.log(`analyze capability=${msg.capability} schema=${msg.resultSchemaVersion}`);
      if (snap.mode === 'RECEIPT_STRUCTURING') {
        result = receiptResult(snap.ocrText ?? '');
      } else if (msg.capability !== 'TEXT_ANALYZER') {
        res.writeHead(400, { 'content-type': 'application/json' });
        res.end(JSON.stringify({ error: 'stub only does TEXT_ANALYZER' }));
        return;
      } else {
        result = synthResult(snap);
      }
    } catch (e) {
      res.writeHead(422, { 'content-type': 'application/json' });
      res.end(JSON.stringify({ error: String(e) }));
      return;
    }
    if (DELAY_MS) await new Promise((r) => setTimeout(r, DELAY_MS));
    res.writeHead(200, { 'content-type': 'application/json' });
    res.end(
      JSON.stringify({ result, provider: 'e2e-stub', model: 'e2e-stub-1', usageCostMicroUsd: 1200 }),
    );
  });
});

server.listen(PORT, '0.0.0.0', () =>
  console.log(`e2e stub provider on :${PORT} delay=${DELAY_MS}ms`),
);
