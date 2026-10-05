// When the transcript follows new output, as pure functions (tested from
// Go: server/web/scroll_test.go). The view follows only while the reader
// is at the bottom; scrolling up, even a little, stops it until they come
// back down or tap "Jump to latest".

// slack is how far from the bottom still counts as at the bottom: a few
// pixels of rounding, not a region that snaps back.
export const slack = 4;

export function atBottom(top: number, height: number, client: number): boolean {
  return height - top - client <= slack;
}

// nextFollow is whether to follow after a scroll the reader made (not one
// the view made to follow): back at the bottom follows again, any move up
// stops following, anything else keeps what was.
export function nextFollow(follow: boolean, lastTop: number, top: number, height: number, client: number): boolean {
  if (atBottom(top, height, client)) return true;
  if (top < lastTop - 0.5) return false;
  return follow;
}

// anchorIndex picks the item that keeps its place on screen while the
// reader is scrolled up: the first one whose bottom is below the top of
// the view. tops and heights are the items' offsets in the transcript.
// -1 when there are none.
export function anchorIndex(tops: number[], heights: number[], top: number): number {
  return firstBelow(tops.length, (i) => tops[i] + heights[i], top);
}

// firstBelow is anchorIndex for n stacked items given by bottom(i), which
// grows with i: a binary search, so a long transcript costs a few reads
// of the layout per scroll event, not one per item.
export function firstBelow(n: number, bottom: (i: number) => number, top: number): number {
  let lo = 0;
  let hi = n - 1;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (bottom(mid) > top) hi = mid;
    else lo = mid + 1;
  }
  return hi;
}

// anchorShift is how far to scroll so the anchor stays where it was: the
// distance it moved because something above it changed height.
export function anchorShift(was: number, now: number): number {
  return Math.abs(now - was) < 0.5 ? 0 : now - was;
}
