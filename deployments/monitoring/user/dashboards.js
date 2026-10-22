/* Same-origin Prometheus graphs; no external libraries or user-provided queries. */
const colors = ['#66d9c1', '#7aa8ff', '#ffcb77', '#f28fb0', '#bca4ff', '#9bde82'];
const panels = [...document.querySelectorAll('.panel')];
const period = document.querySelector('#period');
const ns = 'http://www.w3.org/2000/svg';
function svgNode(tag, attrs, text) {
  const node = document.createElementNS(ns, tag);
  for (const [key, value] of Object.entries(attrs)) node.setAttribute(key, value);
  if (text !== undefined) node.textContent = text;
  return node;
}
function format(value, unit) {
  if (unit === 'percentunit') return (value * 100).toFixed(1) + '%';
  if (unit === 'bytes' || unit === 'Bps') return (value / 1048576).toFixed(1) + (unit === 'bytes' ? ' MiB' : ' MiB/s');
  return new Intl.NumberFormat('ru', {maximumFractionDigits: 3}).format(value) + (unit === 's' ? ' с' : '');
}
async function render(panel, end, duration) {
  const query = panel.dataset.query;
  const link = panel.querySelector('.query');
  link.href = '/query?' + new URLSearchParams({'g0.expr': query, 'g0.tab': '0', 'g0.range_input': duration + 's'});
  const status = panel.querySelector('.status');
  const svg = panel.querySelector('svg');
  const legend = panel.querySelector('.legend');
  try {
    const params = new URLSearchParams({query, start: end - duration, end, step: Math.max(15, Math.ceil(duration / 120)), limit: 25});
    const response = await fetch('/api/v1/query_range?' + params, {signal: AbortSignal.timeout(12000)});
    const body = await response.json();
    if (!response.ok || body.status !== 'success') throw new Error('Запрос не выполнен');
    const series = body.data.result;
    svg.replaceChildren(); legend.replaceChildren();
    const finite = series.flatMap(s => s.values.map(v => Number(v[1])).filter(Number.isFinite));
    if (!finite.length) {status.textContent = 'Нет данных за выбранный период'; return;}
    const low = Math.min(0, ...finite), high = Math.max(...finite);
    const span = high - low || 1;
    svg.append(svgNode('text', {x: 4, y: 14, fill: '#aab8ce', 'font-size': 12}, format(high, panel.dataset.unit)));
    svg.append(svgNode('text', {x: 4, y: 178, fill: '#aab8ce', 'font-size': 12}, format(low, panel.dataset.unit)));
    for (const [x, t, anchor] of [[90, end-duration, 'start'], [710, end, 'end']])
      svg.append(svgNode('text', {x, y: 202, fill: '#aab8ce', 'font-size': 12, 'text-anchor': anchor}, new Date(t*1000).toLocaleTimeString('ru')));
    series.slice(0, 24).forEach((s, i) => {
      let path = '', gap = true;
      for (const [time, raw] of s.values) {
        const value = Number(raw);
        if (!Number.isFinite(value)) {gap = true; continue;}
        const x = 90 + (time - end + duration) / duration * 620;
        const y = 160 - (value - low) / span * 135;
        path += (gap ? 'M' : 'L') + x.toFixed(1) + ',' + y.toFixed(1); gap = false;
      }
      const color = colors[i % colors.length];
      svg.append(svgNode('path', {d: path, stroke: color, fill: 'none', 'stroke-width': 2}));
      const label = document.createElement('div'); label.style.color = color;
      const identity = Object.entries(s.metric).filter(([key]) => key !== '__name__').map(([k,v]) => k + '=' + v).join(' · ');
      const last = Number(s.values.at(-1)?.[1]);
      label.textContent = (identity || s.metric.__name__ || 'Всего') + ': ' + (Number.isFinite(last) ? format(last, panel.dataset.unit) : 'нет текущего значения');
      legend.append(label);
    });
    status.textContent = series.length > 24 ? 'Показаны первые 24 ряда — подробности в запросе' : 'Последнее значение и история';
  } catch (error) {
    svg.replaceChildren(); legend.replaceChildren();
    status.textContent = 'Не удалось обновить график. Откройте запрос для диагностики.';
  }
}
let busy = false;
async function refresh() {
  if (busy) return;
  busy = true;
  const end = Math.floor(Date.now() / 1000), duration = Number(period.value);
  try {
    for (let i = 0; i < panels.length; i += 2) await Promise.all(panels.slice(i, i + 2).map(p => render(p, end, duration)));
    document.querySelector('#updated').textContent = 'Обновлено: ' + new Date().toLocaleTimeString('ru') + ' · каждые 30 секунд · rates за 5 минут';
  } finally {busy = false;}
}
period.addEventListener('change', refresh);
refresh();
setInterval(() => {if (!document.hidden) refresh();}, 30000);
