import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';

import { debounce, el, formatTime } from '../static/app/dom.mjs';
import { loadPage, parseHTML } from './minidom.mjs';

loadPage();

test('el inserts strings as text, never as markup', () => {
  const node = el('p', {}, '<img src=x onerror=alert(1)>');

  assert.equal(node.children.length, 0);
  assert.equal(node.textContent, '<img src=x onerror=alert(1)>');
});

test('el sets attributes, classes and listeners', () => {
  let clicks = 0;
  const node = el('button', {
    type: 'button',
    class: 'small',
    'data-sequence': 7,
    hidden: true,
    disabled: false,
    title: null,
    onclick: () => clicks++,
  });

  node.click();

  assert.equal(node.getAttribute('type'), 'button');
  assert.equal(node.className, 'small');
  assert.equal(node.dataset.sequence, '7');
  assert.equal(node.hidden, true);
  assert.equal(node.disabled, false);
  assert.equal(node.hasAttribute('title'), false);
  assert.equal(clicks, 1);
});

test('el flattens children and skips empty ones', () => {
  const node = el('ul', {}, [el('li', {}, 'a'), null, false, [el('li', {}, 'b')]], undefined);

  assert.deepEqual(node.children.map((child) => child.textContent), ['a', 'b']);
});

test('debounce runs once with the last arguments', async () => {
  const calls = [];
  const debounced = debounce((value) => calls.push(value), 5);

  debounced('a');
  debounced('ab');
  await new Promise((resolve) => setTimeout(resolve, 30));

  assert.deepEqual(calls, ['ab']);
});

test('formatTime shows day, month and time', () => {
  assert.match(formatTime('2026-10-18T12:05:00Z'), /\d{2}:05/);
});

test('every element the client looks up exists in index.html', () => {
  const page = loadPage();
  const dir = new URL('../static/app/', import.meta.url);
  const lookup = /(?:\$|getElementById)\('([\w-]+)'\)/g;
  for (const file of readdirSync(dir).filter((name) => name.endsWith('.mjs'))) {
    for (const [, id] of readFileSync(new URL(file, dir), 'utf8').matchAll(lookup)) {
      assert.ok(page.getElementById(id), `${file} looks up #${id}, which index.html lacks`);
    }
  }
});

test('parseHTML builds the tree of well-formed markup', () => {
  const root = parseHTML('<div id="a"><input name="x" value="1"><p class="hint">text</p></div>');

  const div = root.querySelector('#a');
  assert.equal(div.children.length, 2);
  assert.equal(div.querySelector('input[name="x"]').value, '1');
  assert.equal(div.querySelector('p.hint').textContent, 'text');
  assert.throws(() => parseHTML('<div><p></div>'), /unexpected/);
});
