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
