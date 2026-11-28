// groupSelectors rewrites Tailwind's group-* variants so a hover or focus
// change restyles only the elements that use them (whiteboard#56).
//
// Tailwind v4 builds `group-hover:x` as `.x:is(:where(.group):hover *)`.
// Blink picks what a pseudo-class change restyles by the pseudo-class alone,
// and this rule's subject inside :is() is `*`: so every element that's
// hovered, focused or left has its whole subtree restyled. Written the other
// way round, `:where(.group):hover .x`, Blink restyles only the descendants
// with class `x`. Same meaning, same specificity (one class, one
// pseudo-class).
//
// Stacked variants put one :is() per group on the element:
// `.x:is(G1 *):is(G2 *)` asks for an ancestor matching G1 and one matching
// G2, in either order or the same element.
// - The groups on a pointer or focus state go in front, and the others
//   (`group-enabled:`) stay as they are:
//   `:where(.group):hover .x:is(:where(.group):enabled *)`. The same meaning
//   and specificity. A change of `:enabled` (rare) still restyles a subtree;
//   a hover restyles only the `.x` elements under it.
// - With several in front (`group-hover:group-focus-within:`), it's the list
//   of every way they can sit on one line of ancestors:
//   `G1G2 .x, G1 G2 .x, G2 G1 .x`, each of the original's specificity. Blink
//   restyles the hovered element itself for the ones where a group sits above
//   the hovered one, but never a whole subtree.
// (The list would do for the first kind too, but costs that restyle of the
// hovered element even where nothing changes, as on a disabled tile.)
//
// Runs as a PostCSS step in Vite (css.postcss in vite.config.ts), which comes
// after @tailwindcss/vite has built the utilities. Selectors it can't rewrite
// to the same meaning (a group :is() past the first compound, or an `&`
// past the start) are left as they are; scripts/group-selectors-check.ts fails the
// check if the built CSS keeps any.

/** The group compound of a group variant's `:is(<group> *)`, or null. */
const groupInner = /^(:where\(\.group(?:\\\/[\w-]+)?\)(?:[\w-]|:[\w-]+(?:\([^()\s]*\))?)*) \*$/;

/** The index of the `)` closing the `(` at open. */
function closing(s: string, open: number) {
  let depth = 0;
  for (let i = open; i < s.length; i++) {
    const c = s[i];
    if (c === "\\") i++;
    else if (c === "(" || c === "[") depth++;
    else if (c === ")" || c === "]") {
      if (--depth === 0) return i;
    }
  }
  return -1;
}

/** Where the first compound of a complex selector ends (its first top-level combinator or space). */
function firstCompoundEnd(s: string) {
  let depth = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (c === "\\") i++;
    else if (c === "(" || c === "[") depth++;
    else if (c === ")" || c === "]") depth--;
    else if (depth === 0 && (c === " " || c === ">" || c === "+" || c === "~")) return i;
  }
  return s.length;
}

/** A group compound on a state the pointer or focus changes. */
const interactive = /:(?:hover|focus|focus-visible|focus-within|active)(?![\w-])/;

/** Every ordered set partition of items: each way to lay them on one line of ancestors, some on the same one. */
function orderedPartitions<T>(items: T[]): T[][][] {
  if (!items.length) return [[]];
  const [first, ...rest] = items;
  const out: T[][][] = [];
  for (const p of orderedPartitions(rest)) {
    // first on an ancestor of its own, at each place in the line,
    for (let i = 0; i <= p.length; i++) out.push([...p.slice(0, i), [first], ...p.slice(i)]);
    // or on the same element as another block.
    for (let i = 0; i < p.length; i++) out.push(p.map((b, j) => (j === i ? [first, ...b] : b)));
  }
  return out;
}

/** One complex selector, rewritten as a list (joined with ", "), or unchanged. */
export function rewriteGroupSelector(sel: string): string {
  if (!sel.includes(":where(.group")) return sel;
  // Nested CSS (Tailwind's output before it's optimized, as in the dev
  // server): `&:is(G *)` turns into `G &`, which means the same at any depth
  // of nesting. An `&` anywhere but at the start isn't rewritten.
  if (sel.includes("&") && (sel[0] !== "&" || sel.indexOf("&", 1) >= 0)) return sel;
  // The groups found, each with the :is() it came from.
  const groups: { g: string; text: string }[] = [];
  let rest = "";
  let i = 0;
  let found = false;
  const end = firstCompoundEnd(sel);
  while (i < sel.length) {
    if (sel.startsWith(":is(", i)) {
      const close = closing(sel, i + 3);
      const m = close > 0 ? groupInner.exec(sel.slice(i + 4, close)) : null;
      if (m) {
        if (i >= end) return sel; // past the first compound: leave it
        groups.push({ g: m[1], text: sel.slice(i, close + 1) });
        rest += `\u0000${groups.length - 1}\u0000`;
        found = true;
        i = close + 1;
        continue;
      }
    }
    rest += sel[i];
    i++;
  }
  if (!found) return sel;
  // Any group :is() left (past the first compound) means it isn't safe.
  if (/:is\(:where\(\.group/.test(rest)) return sel;
  const live = groups.filter((x) => interactive.test(x.g));
  // In front: the interactive groups (with none, every group).
  const front = live.length ? live : groups;
  let subject = rest.replace(/\u0000(\d+)\u0000/g, (_, k) => (front.includes(groups[Number(k)]) ? "" : groups[Number(k)].text));
  if (subject === "" || /^[\s>+~]|^::/.test(subject)) subject = `*${subject}`;
  return orderedPartitions(front.map((x) => x.g))
    .map((line) => `${line.map((b) => b.join("")).join(" ")} ${subject}`)
    .join(", ");
}

/** A selector list's selectors, split at the commas outside brackets. */
export function selectorsOf(list: string) {
  const out: string[] = [];
  let depth = 0;
  let start = 0;
  for (let i = 0; i < list.length; i++) {
    const c = list[i];
    if (c === "\\") i++;
    else if (c === "(" || c === "[") depth++;
    else if (c === ")" || c === "]") depth--;
    else if (c === "," && depth === 0) {
      out.push(list.slice(start, i).trim());
      start = i + 1;
    }
  }
  out.push(list.slice(start).trim());
  return out;
}

/** A whole selector list, each selector rewritten. */
export function rewriteGroupSelectors(list: string): string {
  if (!list.includes(":where(.group")) return list;
  const own = selectorsOf(list);
  const next = own.map(rewriteGroupSelector);
  return next.every((s, i) => s === own[i]) ? list : next.join(", ");
}

/** The PostCSS plugin, for Vite's css.postcss.plugins. */
export function groupSelectors() {
  return {
    postcssPlugin: "toru-group-selectors",
    Rule(rule: { selector: string }) {
      const next = rewriteGroupSelectors(rule.selector);
      if (next !== rule.selector) rule.selector = next;
    },
  };
}
groupSelectors.postcss = true as const;
