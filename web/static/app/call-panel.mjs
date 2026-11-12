// The call bar above the workspace: an incoming or outgoing call, its state
// and its buttons, and the <audio> element that plays the peer.

const $ = (id) => document.getElementById(id);

const BUTTONS = ['call-accept', 'call-reject', 'call-cancel', 'call-end', 'call-dismiss'];

function statusText(view, name) {
  if (view.state === 'ended') return 'Звонок завершён';
  if (view.state === 'starting') return 'Включаем микрофон…';
  if (view.state === 'ringing') {
    if (view.role === 'callee') return view.pending === 'accept' ? 'Подключаемся…' : `Входящий звонок от ${name}`;
    return view.owned ? `Вызываем ${name}…` : 'Исходящий вызов с другого устройства';
  }
  if (!view.owned) return 'Звонок идёт на другом устройстве';
  if (view.state === 'connecting') return 'Соединение…';
  return view.remote ? 'Идёт разговор' : 'Идёт разговор, ждём звук собеседника…';
}

// visibleButtons lists what the user can do with the call in its state.
function visibleButtons(view) {
  switch (view.state) {
    case 'ringing':
      if (view.role === 'callee') return ['call-accept', 'call-reject'];
      return view.owned ? ['call-cancel'] : [];
    case 'connecting':
    case 'active':
      return ['call-end'];
    case 'ended':
      return ['call-dismiss'];
    default:
      return [];
  }
}

// bindCallPanel shows calls' view in the page. peerName(view) names the other
// participant.
export function bindCallPanel({ calls, peerName }) {
  const audio = $('call-audio');
  let playing = null;

  const render = () => {
    const view = calls.view;
    const panel = $('call');
    panel.hidden = !view;
    if (!view) {
      panel.dataset.state = '';
      panel.dataset.role = '';
    } else {
      const name = peerName(view) || 'собеседник';
      panel.dataset.state = view.state;
      panel.dataset.role = view.role ?? '';
      $('call-title').textContent = name;
      $('call-status').textContent = statusText(view, name);
      $('call-error').textContent = view.error ?? '';
    }
    const shown = view ? visibleButtons(view) : [];
    for (const id of BUTTONS) {
      $(id).hidden = !shown.includes(id);
      $(id).disabled = Boolean(view?.pending);
    }

    // The peer's audio plays only on the call's device, and stops with the call.
    const remote = view?.owned && view.state !== 'ended' ? view.remote ?? null : null;
    if (remote !== playing) {
      playing = remote;
      audio.srcObject = remote;
      if (remote) audio.play?.()?.catch?.(() => {});
    }
  };

  $('call-accept').addEventListener('click', () => calls.accept());
  $('call-reject').addEventListener('click', () => calls.reject());
  $('call-cancel').addEventListener('click', () => calls.cancel());
  $('call-end').addEventListener('click', () => calls.end());
  $('call-dismiss').addEventListener('click', () => calls.dismiss());
  calls.addEventListener('change', render);
  render();
}
