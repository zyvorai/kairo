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
  $('risks').innerHTML = r.risks.length ? r.risks.map(x => `<div class="risk ${x.severity}"><div class="risk-head"><span class="risk-dot"></span>${escapeHTML(x.title)}</div><p>${escapeHTML(x.resource.kind)}/${escapeHTML(x.resource.namespace)}/${escapeHTML(x.resource.name)} — ${escapeHTML(x.detail)}</p></div>`).join('') : '<div class="risk low"><div class="risk-head"><span class="risk-dot"></span>No blocking findings</div><p>The current deterministic checks did not find a capacity or destructive-storage blocker.</p></div>';
  $('recommendations').innerHTML = r.recommendations.map(x => `<li>${escapeHTML(x)}</li>`).join('');
}
function escapeHTML(s='') { return s.replace(/[&<>'"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c])); }

async function runSimulation() {
  setBusy(true);
  try {
    const response = await fetch('/api/v1/simulate', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({clusterYaml:$('cluster-editor').value, desiredYaml:$('desired-editor').value})});
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
