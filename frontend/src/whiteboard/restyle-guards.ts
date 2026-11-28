// Keeps typing, votes and drops from restyling the whole board
// (whiteboard#37). Imported once by the whiteboard window
// (routes/Whiteboard.tsx), before the board renders.
//
// UI SDK's :has() rules (@ui-sdk/core/dist/web.css) make Blink restyle every
// [data-slot] and svg under the board, 20–30 ms on a full board, whenever an
// element is put into or taken out of the page anywhere, or an element's last
// text is taken out (a textarea's words rewritten). Text added, or changed in
// place, costs nothing. So the window avoids the first two where nothing on
// screen changes; see docs/whiteboard.md, Frame times.
import { prepareAnnouncements } from "@ui-sdk/core";

// UI SDK's announcer (what screen readers hear: "Moved", a vote) makes its
// two live regions on its first announcement, which is the first drop or
// vote on the board. Made now, while the window opens, instead.
prepareAnnouncements(document);

// A <textarea>'s defaultValue is its text node: setting it replaces the node,
// even with the same words. React sets it on every render of an uncontrolled
// field (UI SDK's hidden form fields, like a Select's in the right panel),
// and on every keystroke of a controlled one (each block's text, right after
// setting .value to the same words).
//
// So, in the whiteboard window only (its own page), a write is skipped when
// it changes nothing: when the field already has that default, or when it
// already shows those words and isn't in a form. A field shows its value
// once it has been typed in or given one (React gives every controlled field
// its value first), and its default only matters to a form's reset, so
// skipping it there changes nothing anyone sees.
const desc = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "defaultValue");

if (desc?.get && desc.set && !(desc.set as { guarded?: boolean }).guarded) {
  const { get, set } = desc;
  const guarded = function (this: HTMLTextAreaElement, value: unknown) {
    const next = String(value);
    if (next === get.call(this)) return;
    if (!this.form && next === this.value) return;
    set.call(this, next);
  };
  Object.assign(guarded, { guarded: true });
  Object.defineProperty(HTMLTextAreaElement.prototype, "defaultValue", { ...desc, set: guarded });
}

// Rules this window can't match, taken out of its style sheets
// (whiteboard#51). Blink keys what a hover or focus change restyles by the
// pseudo-class alone, so a rule like Tailwind's `group-hover:` utility as
// Tailwind builds it, `.x:is(:where(.group):hover *)`, restyled the whole
// subtree of every element that's hovered, focused or left, a block's 30–40
// elements on each click and on each move onto a block. The build now turns
// those round (`:where(.group):hover .x`, group-selectors.ts, whiteboard#56),
// so they restyle only elements with their class; they're still taken out
// here, since none can match in this window. UI SDK's Tree rule
// (`[data-slot="tree-item"]:focus-visible > [data-slot="tree-label"]`)
// restyles every [data-slot] in a block whose focus changes, the click's two
// blocks. Nothing in this window has a Tailwind `group` class or a Tree:
// the Toru pages that use them are other windows', sharing one style sheet.
// Other rules in the same list are kept.
const unmatchable = [
  // Tailwind's group-* variants, named groups (group/edge) included, in
  // either form.
  /:where\(\.group(?:\\\/[\w-]+)?\)/,
  // UI SDK's Tree item's focus ring on its label.
  /\[data-slot="tree-item"\]\)?:focus-visible/,
];

/** A selector list's selectors, split at the commas outside brackets. */
function selectorsOf(list: string) {
  const out: string[] = [];
  let depth = 0;
  let start = 0;
  for (let i = 0; i < list.length; i++) {
    const c = list[i];
    if (c === "(" || c === "[") depth++;
    else if (c === ")" || c === "]") depth--;
    else if (c === "," && depth === 0) {
      out.push(list.slice(start, i).trim());
      start = i + 1;
    }
  }
  out.push(list.slice(start).trim());
  return out;
}

const pruned = new WeakSet<CSSStyleSheet>();

function prune(rules: CSSRuleList, parent: string | null, remove: (i: number) => void) {
  for (let i = rules.length - 1; i >= 0; i--) {
    const rule = rules[i];
    if (rule instanceof CSSStyleRule) {
      // A nested rule's selectors read with its parent's in place of `&`.
      const own = selectorsOf(rule.selectorText);
      const full = (s: string) => (parent === null ? s : s.includes("&") ? s.replaceAll("&", `:is(${parent})`) : `:is(${parent}) ${s}`);
      const kept = own.filter((s) => !unmatchable.some((re) => re.test(full(s))));
      if (!kept.length) {
        remove(i);
        continue;
      }
      if (kept.length < own.length) rule.selectorText = kept.join(", ");
      if (rule.cssRules.length) prune(rule.cssRules, full(rule.selectorText), (j) => rule.deleteRule(j));
    } else if (rule instanceof CSSGroupingRule) {
      prune(rule.cssRules, parent, (j) => rule.deleteRule(j));
    }
  }
}

function pruneSheets() {
  for (const sheet of Array.from(document.styleSheets)) {
    if (pruned.has(sheet)) continue;
    let rules: CSSRuleList;
    try {
      rules = sheet.cssRules;
    } catch {
      continue; // not loaded yet, or another origin's
    }
    pruned.add(sheet);
    prune(rules, null, (i) => sheet.deleteRule(i));
  }
}

pruneSheets();
// Sheets that come later: a <link> that loads after this runs, and in the
// dev server each module's <style>.
new MutationObserver(pruneSheets).observe(document.head, { childList: true, subtree: true, characterData: true });
document.addEventListener("load", (e) => e.target instanceof HTMLLinkElement && pruneSheets(), true);
