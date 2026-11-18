// Tabs: adjacent code blocks that each have a title (```sh title="Codex") become one
// block with a tab per title, so the Markdown stays plain: on GitHub and in the
// .md copies they read as labelled blocks one after another. Every group on a
// page switches together, and the choice is remembered, so a reader who picks
// Codex sees Codex everywhere.
const KEY = 'workly-docs-tab';

function titled(el) {
  return el?.classList.contains('expressive-code') && el.querySelector(':scope > figure.has-title') ? el : null;
}

function groups() {
  const found = [];
  for (const block of document.querySelectorAll('.sl-markdown-content > .expressive-code')) {
    if (!titled(block) || titled(block.previousElementSibling)) continue;
    const run = [block];
    for (let next = block.nextElementSibling; titled(next); next = next.nextElementSibling) run.push(next);
    if (run.length > 1) found.push(run);
  }
  return found;
}

const all = [];

function select(label) {
  for (const { buttons, blocks } of all) {
    if (!buttons.some((b) => b.textContent === label)) continue;
    buttons.forEach((b, i) => {
      const on = b.textContent === label;
      b.setAttribute('aria-selected', String(on));
      b.tabIndex = on ? 0 : -1;
      blocks[i].hidden = !on;
    });
  }
}

for (const blocks of groups()) {
  const wrap = document.createElement('div');
  wrap.className = 'code-tabs';
  const bar = document.createElement('div');
  bar.className = 'code-tabs-bar not-content';
  bar.setAttribute('role', 'tablist');
  blocks[0].before(wrap);
  wrap.append(bar, ...blocks);
  const buttons = blocks.map((block, i) => {
    const caption = block.querySelector(':scope > figure > figcaption');
    const b = document.createElement('button');
    b.type = 'button';
    b.setAttribute('role', 'tab');
    b.textContent = caption.querySelector('.title').textContent;
    b.addEventListener('click', () => {
      select(b.textContent);
      try {
        localStorage.setItem(KEY, b.textContent);
      } catch {}
    });
    caption.hidden = true;
    block.setAttribute('role', 'tabpanel');
    block.hidden = i !== 0;
    bar.append(b);
    return b;
  });
  buttons.forEach((b, i) => b.setAttribute('aria-selected', String(i === 0)));
  all.push({ buttons, blocks });
}

try {
  const saved = localStorage.getItem(KEY);
  if (saved) select(saved);
} catch {}

// A ```prompt block is text to paste into an agent: people copy it more than
// they read it, so it shows its first lines, faded, until expanded. The copy
// button still copies all of it.
for (const pre of document.querySelectorAll('.sl-markdown-content .expressive-code pre[data-language="prompt"]')) {
  const block = pre.closest('.expressive-code');
  block.classList.add('prompt-block', 'collapsed');
  const button = document.createElement('button');
  button.type = 'button';
  button.className = 'prompt-toggle not-content';
  button.textContent = 'Show all';
  button.setAttribute('aria-expanded', 'false');
  button.addEventListener('click', () => {
    const collapsed = block.classList.toggle('collapsed');
    button.textContent = collapsed ? 'Show all' : 'Show less';
    button.setAttribute('aria-expanded', String(!collapsed));
  });
  block.after(button);
}
