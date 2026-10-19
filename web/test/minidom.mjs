// A minimal DOM for testing the client's UI modules in Node: it parses the
// real index.html, so tests see the same elements and IDs as the browser.
// Only what the client uses is implemented. Text is never parsed as
// markup, as with textContent and append() in a browser.

import { readFileSync } from 'node:fs';

const VOID_ELEMENTS = new Set(['meta', 'link', 'input', 'br', 'img']);

export class FakeElement extends EventTarget {
  constructor(tagName, attributes = {}) {
    super();
    this.tagName = tagName.toUpperCase();
    this.parentNode = null;
    this.childNodes = [];
    this.attributes = new Map(Object.entries(attributes));
    this.dataset = {};
    this.value = attributes.value ?? '';
    this.checked = 'checked' in attributes;
    this.disabled = 'disabled' in attributes;
    this.hidden = 'hidden' in attributes;
    this.open = 'open' in attributes;
    this.files = [];
    this.scrollTop = 0;
    this.scrollHeight = 0;
    this.clientHeight = 0;
  }

  get id() {
    return this.attributes.get('id') ?? '';
  }

  get className() {
    return this.attributes.get('class') ?? '';
  }

  set className(value) {
    this.attributes.set('class', String(value));
  }

  get children() {
    return this.childNodes.filter((node) => node instanceof FakeElement);
  }

  get textContent() {
    return this.childNodes.map((node) => (typeof node === 'string' ? node : node.textContent)).join('');
  }

  set textContent(value) {
    this.replaceChildren();
    if (value !== '' && value !== null && value !== undefined) this.childNodes.push(String(value));
  }

  append(...nodes) {
    for (const node of nodes) {
      if (node instanceof FakeElement) {
        node.remove();
        node.parentNode = this;
        this.childNodes.push(node);
      } else {
        this.childNodes.push(String(node));
      }
    }
  }

  replaceChildren(...nodes) {
    for (const child of this.children) child.parentNode = null;
    this.childNodes = [];
    this.append(...nodes);
  }

  remove() {
    if (!this.parentNode) return;
    this.parentNode.childNodes = this.parentNode.childNodes.filter((node) => node !== this);
    this.parentNode = null;
  }

  setAttribute(name, value) {
    const text = String(value);
    if (name === 'hidden') this.hidden = true;
    else if (name === 'disabled') this.disabled = true;
    else if (name.startsWith('data-')) this.dataset[camelCase(name.slice(5))] = text;
    this.attributes.set(name, text);
  }

  getAttribute(name) {
    if (name === 'hidden') return this.hidden ? '' : null;
    return this.attributes.get(name) ?? null;
  }

  hasAttribute(name) {
    return this.getAttribute(name) !== null;
  }

  // click dispatches a click unless the element is disabled, as browsers do.
  click() {
    if (!this.disabled) this.dispatchEvent(new Event('click'));
  }

  focus() {
    this.ownerDocument.activeElement = this;
  }

  get ownerDocument() {
    let node = this;
    while (node.parentNode) node = node.parentNode;
    return node.document ?? {};
  }

  *descendants() {
    for (const child of this.children) {
      yield child;
      yield* child.descendants();
    }
  }

  querySelectorAll(selector) {
    return [...this.descendants()].filter((element) => matches(element, selector));
  }

  querySelector(selector) {
    return this.querySelectorAll(selector)[0] ?? null;
  }

  closest(selector) {
    for (let node = this; node; node = node.parentNode) {
      if (matches(node, selector)) return node;
    }
    return null;
  }

  // elements gives a form's named controls, as HTMLFormElement does.
  get elements() {
    const controls = {};
    for (const element of this.descendants()) {
      const name = element.attributes.get('name');
      if (name) controls[name] = element;
    }
    return controls;
  }

  // reset restores a form's controls to their initial values.
  reset() {
    for (const element of this.descendants()) {
      element.value = element.attributes.get('value') ?? '';
      element.checked = element.attributes.has('checked');
    }
  }
}

// matches supports the selectors the client uses: tag, #id, .class and
// [attr="value"], alone or combined in that order.
function matches(element, selector) {
  const parts = /^([a-z0-9]+)?(?:#([\w-]+))?(?:\.([\w-]+))?(?:\[([\w-]+)="([^"]*)"\])?$/i.exec(selector);
  if (!parts) throw new Error(`unsupported selector ${selector}`);
  const [, tag, id, className, attr, value] = parts;
  return (
    (!tag || element.tagName === tag.toUpperCase()) &&
    (!id || element.id === id) &&
    (!className || element.className.split(/\s+/).includes(className)) &&
    (!attr || element.attributes.get(attr) === value)
  );
}

function camelCase(name) {
  return name.replace(/-([a-z])/g, (_, letter) => letter.toUpperCase());
}

// parseHTML builds an element tree from well-formed markup such as the
// client's index.html. Whitespace-only text is dropped.
export function parseHTML(html) {
  const root = new FakeElement('#document');
  const stack = [root];
  const token = /<!--[\s\S]*?-->|<!doctype[^>]*>|<\/([a-z0-9]+)\s*>|<([a-z0-9]+)((?:\s+[^\s=>/]+(?:="[^"]*")?)*)\s*\/?>|([^<]+)/gi;
  for (const [, closing, tag, rawAttributes, text] of html.matchAll(token)) {
    const parent = stack.at(-1);
    if (closing) {
      if (parent.tagName !== closing.toUpperCase()) throw new Error(`unexpected </${closing}> in ${parent.tagName}`);
      stack.pop();
    } else if (tag) {
      const attributes = {};
      for (const [, name, value] of (rawAttributes ?? '').matchAll(/([^\s=]+)(?:="([^"]*)")?/g)) {
        attributes[name] = value ?? '';
      }
      const element = new FakeElement(tag, attributes);
      parent.append(element);
      if (!VOID_ELEMENTS.has(tag.toLowerCase())) stack.push(element);
    } else if (text && text.trim()) {
      parent.append(text.trim());
    }
  }
  if (stack.length !== 1) throw new Error(`unclosed <${stack.at(-1).tagName}>`);
  return root;
}

// loadPage parses web/static/index.html and installs it as the global
// document, with a window whose confirm() answers confirmAnswer.
export function loadPage({ confirmAnswer = true } = {}) {
  const html = readFileSync(new URL('../static/index.html', import.meta.url), 'utf8');
  const root = parseHTML(html);
  const document = {
    activeElement: null,
    body: root.querySelector('body'),
    getElementById: (id) => root.querySelector(`#${id}`),
    createElement: (tag) => new FakeElement(tag),
  };
  root.document = document;
  globalThis.document = document;
  globalThis.window = {
    confirm: () => confirmAnswer,
    location: { protocol: 'http:', host: 'localhost:8080' },
  };
  return document;
}

// settle lets pending promise callbacks and zero-delay timers run.
export const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
