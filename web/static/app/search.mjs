// User search by name prefix, shared by the sidebar and the members panel.

import { debounce, el } from './dom.mjs';
import { describeError } from './errors.mjs';

// bindUserSearch lists the users whose names start with input's value and
// calls pick(user) when one is chosen. Answers to outdated queries, and to
// queries of a scope that has ended, are dropped.
export function bindUserSearch({ input, list, errorLine, scope, pick, delayMs }) {
  let latest = 0;
  const search = debounce(async () => {
    const query = ++latest;
    const current = scope();
    errorLine.textContent = '';
    try {
      const users = await current.profiles.search(input.value);
      if (query !== latest || !current.alive) return;
      list.replaceChildren(...users.map((user) =>
        el('li', {}, el('button', { type: 'button', class: 'user-item', onclick: () => pick(user) }, user.username)),
      ));
    } catch (err) {
      if (query === latest && current.alive) errorLine.textContent = describeError(err);
    }
  }, delayMs);
  input.addEventListener('input', search);
}
