// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

const $ = (id) => document.getElementById(id);
let demo = {clusterYaml:'', desiredYaml:''};

function humanBytes(n) {
  const sign = n < 0 ? '-' : '+';
  n = Math.abs(n);
  const units = ['B','KiB','MiB','GiB','TiB'];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${sign}${n.toFixed(i > 2 ? 1 : 0)} ${units[i]}`;
}
function signed(v, suffix='') { return `${v > 0 ? '+' : ''}${v}${suffix}`; }

async function loadDemo() {
  const response = await fetch('/api/v1/examples');
  if (!response.ok) throw new Error('Demo examples are unavailable. Run the binary from the repository root or provide your own YAML.');
  demo = await response.json();
  $('cluster-editor').value = demo.clusterYaml;
  $('desired-editor').value = demo.desiredYaml;
  $('object-count').textContent = 'Demo manifests loaded';
  syncNodeHint();
}

function setBusy(busy) {
  const button = $('run');
  button.disabled = busy;
  button.style.opacity = busy ? '.62' : '1';
  button.firstChild.textContent = busy ? 'Simulating… ' : 'Run simulation ';
}

function showResult(r) {
  $('result-empty').classList.add('hidden');
  $('result-content').classList.remove('hidden');
  $('verdict').textContent = r.verdict;
  $('verdict').style.color = r.verdict === 'BLOCK' ? '#d70015' : r.verdict === 'REVIEW' ? '#b25000' : '#15803d';
  $('cluster-name').textContent = `${r.clusterName} · ${r.riskLevel} risk`;
  $('score').textContent = r.blastRadius;
  const ring = $('score-ring'); ring.style.setProperty('--score', r.blastRadius);
  ring.style.background = `conic-gradient(${r.blastRadius >= 60 ? '#ff453a' : r.blastRadius >= 30 ? '#ff9f0a' : '#30d158'} ${r.blastRadius}%, #eee 0)`;
  $('unsched').textContent = r.unschedulablePods;
  $('restarted').textContent = r.podsRestarted;
  $('services').textContent = r.servicesAffected;
  $('pvc').textContent = r.pvcDestructiveRisks;
  $('cpu-delta').textContent = signed(r.cpuDeltaCores, ' cores');
  $('mem-delta').textContent = humanBytes(r.memoryDeltaBytes);
  $('gpu-delta').textContent = signed(r.gpuDelta);
  $('object-count').textContent = `${r.objectsAnalyzed} objects analyzed · ${r.changedObjects} changed · ${r.createdObjects} created`;
  $('risks').innerHTML = r.risks.length ? r.risks.map(x => `<div class="risk ${escapeHTML(x.severity)}"><div class="risk-head"><span class="risk-dot"></span>${escapeHTML(x.title)}</div><p>${escapeHTML(x.resource.kind)}/${escapeHTML(x.resource.namespace)}/${escapeHTML(x.resource.name)} — ${escapeHTML(x.detail)}</p></div>`).join('') : '<div class="risk low"><div class="risk-head"><span class="risk-dot"></span>No blocking findings</div><p>The current deterministic checks did not find a capacity or destructive-storage blocker.</p></div>';
  showCost(r.cost);
  $('recommendations').innerHTML = r.recommendations.map(x => `<li>${escapeHTML(x)}</li>`).join('');
}
function escapeHTML(s='') { return s.replace(/[&<>'"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c])); }

async function runSimulation() {
  setBusy(true);
  try {
    const response = await fetch('/api/v1/simulate', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({clusterYaml:$('cluster-editor').value, desiredYaml:$('desired-editor').value, prices:prices()})});
    const data = await response.json();
    if (!response.ok) throw new Error(data.error || 'Simulation failed');
    showResult(data);
  } catch (e) {
    $('result-empty').classList.remove('hidden'); $('result-content').classList.add('hidden');
    $('result-empty').innerHTML = `<div class="empty-glyph">!</div><h3>Couldn’t simulate.</h3><p>${escapeHTML(e.message)}</p>`;
  } finally { setBusy(false); }
}

document.querySelectorAll('.tab').forEach(btn => btn.addEventListener('click', () => {
  document.querySelectorAll('.tab').forEach(x => x.classList.remove('active')); btn.classList.add('active');
  const cluster = btn.dataset.tab === 'cluster'; $('cluster-editor').classList.toggle('hidden', !cluster); $('desired-editor').classList.toggle('hidden', cluster);
}));
$('load-demo').addEventListener('click', async () => { await loadDemo(); $('result-empty').classList.remove('hidden'); $('result-content').classList.add('hidden'); });
$('run').addEventListener('click', runSimulation);
document.addEventListener('keydown', e => { if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') runSimulation(); });
loadDemo().then(runSimulation).catch(e => { $('object-count').textContent = e.message; });

// ---- Explore views: drain, consolidation, rollout surge, API removal, change list ----
// Everything interpolated into HTML goes through esc(); numbers are coerced with num().
const esc = (v) => escapeHTML(String(v == null ? '' : v));
const num = (v) => { const n = Number(v); return Number.isFinite(n) ? n : 0; };
const pct = (v) => `${Math.round(num(v) * 100)}%`;
function bytes(n) { return humanBytes(num(n)).replace(/^\+/, ''); }
const verdictColor = (v) => v === 'BLOCK' ? '#d70015' : v === 'REVIEW' ? '#b25000' : '#15803d';

function prices() {
  const node = parseFloat($('price-node').value), gib = parseFloat($('price-gib').value);
  const p = {};
  if (node > 0) p.nodeHourPrice = node;
  if (gib > 0) p.gibHourPrice = gib;
  if (!p.nodeHourPrice && !p.gibHourPrice) return undefined;
  const cur = $('price-cur').value.trim();
  if (cur) p.currency = cur;
  return p;
}
function costHTML(c) {
  if (!c) return '';
  const sign = num(c.monthlyDelta) > 0 ? '+' : '';
  return `<div class="cost-box"><span class="label">Estimated monthly cost change</span><b>${sign}${num(c.monthlyDelta).toFixed(2)} ${esc(c.currency)}</b><p>Nodes ${num(c.nodeMonthlyDelta).toFixed(2)} · memory ${num(c.memoryMonthlyDelta).toFixed(2)} · ${esc(c.basis)}. ${esc(c.note)}</p></div>`;
}
function showCost(c) {
  $('cost-box').classList.toggle('hidden', !c);
  if (!c) return;
  const sign = num(c.monthlyDelta) > 0 ? '+' : '';
  $('cost-amount').textContent = `${sign}${num(c.monthlyDelta).toFixed(2)} ${c.currency}`;
  $('cost-note').textContent = `${c.basis}. ${c.note}`;
}
function riskHTML(r) {
  return `<div class="risk ${esc(r.severity)}"><div class="risk-head"><span class="risk-dot"></span>${esc(r.title)} <small>${esc(r.code)}</small></div><p>${esc(r.resource.kind)}/${esc(r.resource.namespace)}/${esc(r.resource.name)} — ${esc(r.detail)}</p></div>`;
}
function metrics(items) {
  return `<div class="x-metrics">${items.map(([k, v]) => `<div><span>${esc(k)}</span><b>${esc(v)}</b></div>`).join('')}</div>`;
}

function nodeNames(yaml) {
  const out = [];
  for (const doc of String(yaml).split(/^---\s*$/m)) {
    if (!/^kind:\s*Node\s*$/m.test(doc)) continue;
    const m = doc.match(/^metadata:\s*\{[^}]*\bname:\s*([^\s,}]+)/m) || doc.match(/^\s+name:\s*([^\s#]+)/m);
    if (m) out.push(m[1].replace(/^["']|["']$/g, ''));
  }
  return out;
}
function syncNodeHint() {
  const names = nodeNames($('cluster-editor').value);
  $('node-hint').textContent = names.length ? `Nodes in the snapshot: ${names.join(', ')}` : '';
  if (!$('drain-nodes').value && names.length) $('drain-nodes').value = names[0];
}

function renderDrain(r) {
  const rows = r.pods.map(p => {
    let note = p.blockedBy ? `blocked by PDB ${p.blockedBy}` : p.action === 'evict' ? (p.reschedulable ? 'reschedulable' : 'no node fits') : '';
    const bad = p.blockedBy || (p.action === 'evict' && !p.reschedulable) || p.action === 'unmanaged';
    return `<tr class="${bad ? 'bad' : 'good'}"><td>${esc(p.namespace)}/${esc(p.name)}</td><td>${esc(p.node)}</td><td>${esc(p.action)}</td><td>${esc(note)}</td></tr>`;
  }).join('');
  return `<div class="x-verdict" style="color:${verdictColor(r.verdict)}">${esc(r.verdict)}</div><p class="x-summary">Drain ${esc(r.nodes.join(', '))}</p>` +
    metrics([['Evicted', num(r.evicted)], ['Ignored', num(r.ignored)], ['Unmanaged', num(r.unmanaged)], ['Unschedulable', num(r.unschedulable)], ['PDB-blocked', num(r.pdbBlocked)]]) +
    (rows ? `<table class="x-table"><thead><tr><th>Pod</th><th>Node</th><th>Action</th><th>Outcome</th></tr></thead><tbody>${rows}</tbody></table>` : '<p class="x-note">No pods run on these nodes.</p>') +
    (r.risks.length ? `<div class="x-h">Findings</div><div>${r.risks.map(riskHTML).join('')}</div>` : '');
}

function renderConsolidate(r) {
  const rows = r.nodes.map(n => `<tr class="${n.removable ? 'remove' : 'keep'}"><td>${esc(n.name)}</td><td>${n.removable ? 'Remove' : 'Keep'}</td><td>${num(n.cpuRequested).toFixed(2)} / ${num(n.cpuCapacity).toFixed(2)}</td><td>${bytes(n.memoryRequestedBytes)} / ${bytes(n.memoryCapacityBytes)}</td><td>${esc(n.reason || '')}</td></tr>`).join('');
  const moves = r.moves.map(m => `<tr><td>${esc(m.namespace)}/${esc(m.name)}</td><td>${esc(m.from)}</td><td>${esc(m.to)}</td></tr>`).join('');
  return `<p class="x-summary">${esc(r.summary)}</p>` +
    metrics([['Nodes', `${num(r.nodesBefore)} → ${num(r.nodesAfter)}`], ['CPU reclaimed', `${num(r.cpuReclaimCores)} cores`], ['Memory reclaimed', bytes(r.memoryReclaimBytes)], ['CPU requested', `${pct(r.utilization.cpuBefore)} → ${pct(r.utilization.cpuAfter)}`], ['Memory requested', `${pct(r.utilization.memoryBefore)} → ${pct(r.utilization.memoryAfter)}`]]) +
    costHTML(r.cost) +
    `<table class="x-table"><thead><tr><th>Node</th><th>Plan</th><th>CPU requested</th><th>Memory requested</th><th>Why it stays</th></tr></thead><tbody>${rows}</tbody></table>` +
    (moves ? `<div class="x-h">Pods to reschedule</div><table class="x-table"><thead><tr><th>Pod</th><th>From</th><th>To</th></tr></thead><tbody>${moves}</tbody></table>` : '') +
    `<div class="x-h">Not modelled</div><ul class="x-list">${r.notModelled.map(x => `<li>${esc(x)}</li>`).join('')}</ul>`;
}

function renderRollout(r) {
  const risks = r.risks.filter(x => String(x.code).startsWith('ROLLOUT_'));
  const rolling = r.changes.filter(c => c.action === 'changed' && c.kind === 'Deployment').length;
  return `<p class="x-summary">${risks.length ? `${num(risks.length)} rollout finding(s)` : 'No rollout stall detected'}</p>` +
    metrics([['Rollout risks', num(r.rolloutRisks)], ['Stalled rollouts', num(r.rolloutStalls)], ['Changed Deployments', rolling], ['Pods restarted', num(r.podsRestarted)]]) +
    (risks.length ? `<div>${risks.map(riskHTML).join('')}</div>` : '<p class="x-note">Every changed Deployment’s surge pods (maxSurge) fit alongside the new replicas, or the change does not start a rollout.</p>') +
    '<p class="x-note">Peak capacity is checked with the snapshot’s free resources, requests, selectors and taints. Replica-only changes and Recreate strategies never surge.</p>';
}

function renderAPI(r) {
  const risks = r.risks.filter(x => String(x.code).startsWith('API_'));
  return `<p class="x-summary">Target Kubernetes ${esc(r.targetVersion)}</p>` +
    metrics([['Removed APIs', num(r.apiRemoved)], ['Deprecated APIs', num(r.apiDeprecated)]]) +
    (risks.length ? `<div>${risks.map(riskHTML).join('')}</div>` : `<p class="x-note">All proposed objects use API versions served on ${esc(r.targetVersion)}.</p>`);
}

function renderExplain(r) {
  if (!r.changes.length) return '<p class="x-note">The proposed manifests do not differ from the snapshot.</p>';
  return r.changes.map(c => {
    const rows = c.fields.map(f => `<tr><td>${esc(f.field)}</td><td class="x-old">${esc(f.old)}</td><td class="x-new">${esc(f.new)}</td></tr>`).join('');
    return `<div class="x-h">${esc(c.action)} · ${esc(c.kind)} ${esc(c.namespace)}/${esc(c.name)}</div>` +
      (rows ? `<table class="x-table"><thead><tr><th>Field</th><th>Before</th><th>After</th></tr></thead><tbody>${rows}</tbody></table>` : '<p class="x-note">No tracked fields differ (replicas, images, requests and limits).</p>');
  }).join('');
}

const exploreViews = {
  drain: {path: '/api/v1/drain', render: renderDrain, body: () => ({clusterYaml: $('cluster-editor').value, nodes: $('drain-nodes').value.split(',').map(s => s.trim()).filter(Boolean)}),
    check: () => $('drain-nodes').value.trim() ? '' : 'Enter at least one node name.'},
  consolidate: {path: '/api/v1/consolidate', render: renderConsolidate, body: () => ({clusterYaml: $('cluster-editor').value, prices: prices()})},
  rollout: {path: '/api/v1/simulate', render: renderRollout, body: () => ({clusterYaml: $('cluster-editor').value, desiredYaml: $('desired-editor').value})},
  api: {path: '/api/v1/simulate', render: renderAPI, body: () => ({clusterYaml: $('cluster-editor').value, desiredYaml: $('desired-editor').value, targetVersion: $('api-target').value})},
  explain: {path: '/api/v1/simulate', render: renderExplain, body: () => ({clusterYaml: $('cluster-editor').value, desiredYaml: $('desired-editor').value})},
};
let exploreView = 'drain';

async function runExplore() {
  const view = exploreViews[exploreView];
  const out = $('explore-result'), status = $('explore-status'), button = $('explore-run');
  const problem = view.check ? view.check() : '';
  if (problem) { out.innerHTML = `<p class="x-note">${esc(problem)}</p>`; return; }
  button.disabled = true; status.textContent = 'Simulating…';
  try {
    const response = await fetch(view.path, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(view.body())});
    const data = await response.json();
    if (!response.ok) throw new Error(data.error || 'Simulation failed');
    out.innerHTML = view.render(data);
    status.textContent = '';
  } catch (e) {
    out.innerHTML = `<div class="risk high"><div class="risk-head"><span class="risk-dot"></span>Couldn’t simulate</div><p>${esc(e.message)}</p></div>`;
    status.textContent = '';
  } finally { button.disabled = false; }
}
function selectExplore(name) {
  exploreView = name;
  document.querySelectorAll('[data-explore]').forEach(b => { const on = b.dataset.explore === name; b.classList.toggle('active', on); b.setAttribute('aria-selected', on ? 'true' : 'false'); });
  document.querySelectorAll('[data-pane]').forEach(p => p.classList.toggle('hidden', p.dataset.pane !== name));
  $('explore-result').innerHTML = '';
}
(function initExplore() {
  const sel = $('api-target');
  sel.innerHTML = '<option value="">Newest known (default)</option>' + Array.from({length: 15}, (_, i) => 33 - i).map(m => `<option value="1.${num(m)}">1.${num(m)}</option>`).join('');
  document.querySelectorAll('[data-explore]').forEach(b => b.addEventListener('click', () => selectExplore(b.dataset.explore)));
  $('explore-run').addEventListener('click', runExplore);
  $('cluster-editor').addEventListener('change', syncNodeHint);
})();

// Product-page motion: subtle, progressive, and disabled by reduced-motion preferences.
const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
const navShell = document.getElementById('nav-shell');
function syncNav() { if (navShell) navShell.classList.toggle('scrolled', window.scrollY > 8); }
syncNav();
window.addEventListener('scroll', syncNav, {passive:true});
if (!reduceMotion && 'IntersectionObserver' in window) {
  const observer = new IntersectionObserver((entries) => {
    entries.forEach((entry) => {
      if (entry.isIntersecting) { entry.target.classList.add('in-view'); observer.unobserve(entry.target); }
    });
  }, {threshold:0.12, rootMargin:'0px 0px -50px'});
  document.querySelectorAll('.reveal-on-scroll').forEach((el) => observer.observe(el));
} else {
  document.querySelectorAll('.reveal-on-scroll').forEach((el) => el.classList.add('in-view'));
}
