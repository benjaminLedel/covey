---
name: covey mobile
description: The Flutter companion to the covey control plane — two spaces, Team and Notes, under a floating glass capsule.
colors:
  surface-0: "#F2F2F2"
  surface-1: "#E6E6E6"
  surface-2: "#FFFFFF"
  text-primary: "#16161A"
  text-secondary: "#48484E"
  text-muted: "#5A5A60"
  border: "#00000029"
  hairline: "#0000001A"
  text-accent: "#8F3F18"
  bg-accent: "#F2E8E4"
  text-wait: "#37506E"
  bg-wait: "#ECEFF3"
  text-danger: "#9D2427"
  text-success: "#0E6B45"
  glass: "#FFFFFFB8"
  shadow: "#00000024"
  dark-surface-0: "#121214"
  dark-surface-1: "#0B0B0C"
  dark-surface-2: "#1F1F22"
  dark-text-primary: "#F4F4F5"
  dark-text-secondary: "#B4B4BA"
  dark-text-muted: "#9B9BA1"
  dark-border: "#FFFFFF33"
  dark-hairline: "#FFFFFF1F"
  dark-text-accent: "#F0A184"
  dark-bg-accent: "#2B1A12"
  dark-text-wait: "#9DBBE0"
  dark-bg-wait: "#1C2330"
  dark-text-danger: "#EF8D84"
  dark-text-success: "#4EC98A"
  dark-glass: "#2A2A2EB8"
  dark-shadow: "#00000066"
  clay: "#CC7A5B"
typography:
  display:
    fontFamily: "Inter"
    fontSize: "34px"
    fontWeight: 700
    lineHeight: 1.12
    letterSpacing: "-0.9px"
  headline:
    fontFamily: "Inter"
    fontSize: "26px"
    fontWeight: 700
    lineHeight: 1.18
    letterSpacing: "-0.6px"
  title-large:
    fontFamily: "Inter"
    fontSize: "20px"
    fontWeight: 600
    lineHeight: 1.25
    letterSpacing: "-0.3px"
  title:
    fontFamily: "Inter"
    fontSize: "17px"
    fontWeight: 600
    lineHeight: 1.3
    letterSpacing: "-0.2px"
  body:
    fontFamily: "Inter"
    fontSize: "17px"
    fontWeight: 400
    lineHeight: 1.42
    letterSpacing: "-0.2px"
  body-secondary:
    fontFamily: "Inter"
    fontSize: "15px"
    fontWeight: 400
    lineHeight: 1.4
    letterSpacing: "-0.1px"
  caption:
    fontFamily: "Inter"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: 1.35
  label:
    fontFamily: "Inter"
    fontSize: "15px"
    fontWeight: 600
    lineHeight: 1.3
    letterSpacing: "-0.1px"
  label-small:
    fontFamily: "Inter"
    fontSize: "12px"
    fontWeight: 500
    lineHeight: 1.3
rounded:
  bubble-tail: "8px"
  tile: "11px"
  sidebar-row: "12px"
  control: "14px"
  inset-group: "18px"
  card: "20px"
  bubble: "22px"
  dialog: "24px"
  sheet: "28px"
  pill: "999px"
spacing:
  hairline: "0.6px"
  xs: "4px"
  sm: "10px"
  md: "14px"
  gutter: "16px"
  section-top: "26px"
  capsule-clearance: "112px"
components:
  button-primary:
    backgroundColor: "{colors.text-primary}"
    textColor: "{colors.surface-2}"
    typography: "{typography.label}"
    rounded: "{rounded.control}"
    height: "50px"
  button-primary-pill:
    backgroundColor: "{colors.text-primary}"
    textColor: "{colors.surface-2}"
    typography: "{typography.label}"
    rounded: "{rounded.pill}"
    height: "56px"
  button-outlined:
    backgroundColor: "{colors.surface-2}"
    textColor: "{colors.text-primary}"
    typography: "{typography.label}"
    rounded: "{rounded.control}"
    height: "50px"
  button-text:
    textColor: "{colors.text-accent}"
    typography: "{typography.label}"
    height: "44px"
  button-answer:
    backgroundColor: "{colors.text-wait}"
    textColor: "{colors.surface-2}"
    rounded: "{rounded.pill}"
    padding: "0 18px"
    height: "40px"
  input:
    backgroundColor: "{colors.surface-2}"
    textColor: "{colors.text-primary}"
    typography: "{typography.body}"
    rounded: "{rounded.control}"
    padding: "14px 16px"
  inset-group:
    backgroundColor: "{colors.surface-2}"
    rounded: "{rounded.inset-group}"
  group-row:
    typography: "{typography.title}"
    padding: "10px 14px"
    height: "62px"
  wait-card:
    backgroundColor: "{colors.surface-2}"
    rounded: "{rounded.card}"
    padding: "16px 12px 16px 16px"
  bubble-person:
    backgroundColor: "{colors.text-primary}"
    textColor: "{colors.surface-2}"
    typography: "{typography.body}"
    rounded: "{rounded.bubble}"
    padding: "11px 16px"
  bubble-agent:
    backgroundColor: "{colors.surface-2}"
    textColor: "{colors.text-primary}"
    typography: "{typography.body}"
    rounded: "{rounded.bubble}"
    padding: "11px 16px"
  bubble-question:
    backgroundColor: "{colors.bg-wait}"
    textColor: "{colors.text-wait}"
    typography: "{typography.body}"
    rounded: "{rounded.bubble}"
    padding: "11px 16px"
  capsule:
    backgroundColor: "{colors.glass}"
    rounded: "{rounded.pill}"
    padding: "5px"
  capsule-item-selected:
    textColor: "{colors.text-primary}"
    typography: "{typography.label}"
    rounded: "{rounded.pill}"
    padding: "0 18px"
    height: "48px"
  capsule-add:
    backgroundColor: "{colors.glass}"
    textColor: "{colors.text-accent}"
    rounded: "{rounded.pill}"
    size: "58px"
  kind-mark:
    backgroundColor: "{colors.surface-0}"
    textColor: "{colors.text-secondary}"
    rounded: "{rounded.tile}"
    size: "36px"
  kind-mark-meeting:
    backgroundColor: "{colors.bg-accent}"
    textColor: "{colors.text-accent}"
    rounded: "{rounded.tile}"
    size: "36px"
  sidebar:
    backgroundColor: "{colors.surface-1}"
    width: "232px"
  sidebar-row-selected:
    backgroundColor: "{colors.surface-2}"
    textColor: "{colors.text-primary}"
    typography: "{typography.label}"
    rounded: "{rounded.sidebar-row}"
    padding: "11px 12px"
---

# Design System: covey mobile

Scope: the Flutter app under `mobile/` only. The web interface at the repository root has its own implementation and is not described here. The source of truth is `mobile/lib/theme.dart` (`CoveyColors`, the Inter scale) and `mobile/lib/ui.dart` (the space primitives); the values are the web's tokens carried over, light and dark.

## Overview

**Creative North Star: "Two Spaces and a Capsule"**

The app is two spaces, Team and Notes, each a single scroll under a large left-aligned title, with a frosted capsule floating over the content to switch between them and a round + beside it for capture. It deliberately reads as a native iOS app rather than stock Material: iOS large-title metrics (44 pt bar, 34 pt title, collapse after ~40 pt of scroll), inset grouped lists, Cupertino icons on Apple platforms, bouncing scroll.

The ground is exactly neutral — paper `#F2F2F2` in light, ink `#121214` in dark — and structure comes from layering (white inset groups and cards on the sheet) and 0.6 px hairlines, never from hue. Colour in a list belongs to the agents' faces, which are computed from each agent's slug; the clay signet sits top left in every space.

On windows 840 pt and wider the capsule becomes a 232 pt sidebar, the list pane is 400 pt, and the thread or note opens beside it.

**Key Characteristics:**
- Neutral sheet, white layers, hairlines; no tinted surfaces except the waiting and accent pairs.
- Inter alone, on a steep scale: 34 / 26 / 20 / 17 / 15 / 13.
- Large left-aligned titles; bar titles appear only once the large title has scrolled away.
- Frosted glass only for controls that float over moving content.
- Agent faces (drawn, slug-derived, with state) as the list's only colour.
- State is always also said in words beside the face or colour.

## Colors

An exactly neutral ground with one warm accent for action, one cool pair for "waiting on you", and red reserved for failure. Every colour exists in a light and a dark value (`dark-*` in the frontmatter); code reads them through `context.colors`, never as literals.

### Primary
- **Ink** (text-primary): body text, and inverted as the fill of primary buttons, the send button and the person's own chat bubbles. Primary action in this app is ink, not clay.
- **Clay Text** (text-accent): the text-capable cut of the clay, for action and activity — the + in the capsule and sidebar, text buttons, the cursor and selection, the focused input border, the summary icon on the "summarise" button, dictation while listening.
- **Clay Wash** (bg-accent): the tile behind a meeting note's kind mark.

### Secondary
- **Waiting Blue** (text-wait / bg-wait): an open question from an agent — the question bubble, its Answer button, the reply banner above the composer, the selection ring on the question being answered.

### Tertiary
- **Failure Red** (text-danger): error text and failed sends. The build also uses it for the dot of a running recording; that use is not part of the system (red means failure).
- **Done Green** (text-success): a checked checklist item in a summary.

### Neutral
- **Paper Sheet** (surface-0): scaffold, app bar, the collapsed bar once scrolled.
- **Sidebar Ground** (surface-1): the wide-window sidebar only.
- **White Layer** (surface-2): inset groups, cards, agent bubbles, inputs, dialogs, sheets, menus, the avatar circle.
- **Secondary / Muted text**: subtitles and captions; muted also carries unselected capsule items and chevrons.
- **Border** and **Hairline**: outlined buttons and drag handles; dividers, input strokes, the glass rim.
- **Glass** and **Shadow**: the capsule fill under its blur (72% surface) and its lift.

### Named Rules
**The Faces Carry the Colour Rule.** In a list, the only hue is the agent's face; rows are white on the sheet, their state is a word in label-small muted type.

**The Ink Acts, Clay Starts Rule.** Committing buttons (save, connect, delete, send) are ink. Clay text marks starting something (+, new, summarise) and interactive text.

**The Clay Is Fixed Rule.** `#CC7A5B` is the signet's own colour and is painted as a literal on the mark only; it does not change with appearance and is not a UI token.

## Typography

**Display Font:** Inter (bundled)
**Body Font:** Inter

**Character:** One family, tightened as it grows (negative tracking from -0.1 to -0.9), so hierarchy comes from size and weight steps the eye tells apart without reading.

### Hierarchy
- **Display** (700, 34px, 1.12): the large title of a space.
- **Headline** (700, 26px, 1.18): a detail screen's title and a note's heading field.
- **Title Large** (600, 20px, 1.25): section titles in a space, dialog titles, the empty-thread title, "covey" in the sidebar.
- **Title** (600, 17px): a row's name, a card's lead line, the collapsed bar title, a summary heading.
- **Body** (400, 17px, 1.42): chat text, card body, note text, space subtitles (in muted).
- **Body Secondary** (400, 15px, 1.4, secondary colour): row subtitles, dialog content.
- **Caption** (400, 13px, muted): "is working on it" line and small meta.
- **Label** (600, 15px): buttons and capsule items. **Label Small** (500, 12px, muted): states in words, inbox type, task tag above a bubble.

### Named Rules
**The Tabular Time Rule.** Times and durations in row subtitles use tabular figures.

## Layout

A single column with a 16 pt side gutter everywhere. A space is `SpaceScroll`: pinned 44 pt bar (signet left in a 60 pt leading slot, avatar and actions right), the large title 2 pt under it, the subtitle 6 pt below, then content, then 112 pt of clearance so the floating capsule never covers the last row. Section titles take 26 pt above and 10 pt below, so they belong to what follows. Rows in an inset group are at least 62 pt tall, 14/10 padding, a 36 pt leading slot and 14 pt gap; dividers are indented 64 pt so they start at the text. Waiting cards stack with 10 pt between them.

Responsive: below 840 pt, one space at a time with the capsule; at 840 pt and wider, sidebar (232) | list pane (400) | detail, separated by 0.6 pt vertical hairlines, and the list pane drops its bar row (`compact`) so its title lines up with the detail. Chat bubbles are capped at 80% of the pane and 560 pt. Touch targets are at least 44 pt.

## Elevation & Depth

Flat by default: depth is tonal layering, white on the neutral sheet (and lighter grey on near-black in dark), with no Material elevation or surface tint anywhere. The one lifted material is glass — a 22 sigma backdrop blur over a 72% surface fill, a 0.8 pt hairline rim and a soft shadow — used for controls that float over content that moves beneath them: the space capsule, the + button, the thread composer and the dictation button.

### Shadow Vocabulary
- **Glass lift** (`0 10px 28px` in the shadow token, 14% black light / 40% black dark): only under glass.

### Named Rules
**The Glass Means Above Rule.** Blur appears only on floating controls; a card, row or sheet never blurs.

## Shapes

Soft, continuous corners that grow with the element's size: 11 on kind tiles, 12 on sidebar rows and the scan frame, 14 on buttons and inputs, 18 on inset groups, 20 on waiting cards and note bodies, 22 on bubbles, 24 on dialogs, 28 on bottom sheets. Floating controls and inline actions are full pills (stadium): the capsule, capsule items, the +, the composer (radius 28), Answer, Save, Connect. Chat bubbles tighten the corner nearest the speaker to 8 — a tail without a tail. Agent faces and the avatar are circles or soft squares drawn in code; the signet is a 64-unit clay tile with radius 14.

## Components

### Buttons
- **Shape:** 14 pt radius at 50 pt height for full-width actions; stadium for inline and hero actions (38–40 pt inline, 56 pt hero).
- **Primary:** ink fill, surface-2 label (Label type).
- **Outlined:** surface-2 fill, border stroke, ink label; used for retry, summarise, the sidebar's "+ New".
- **Text:** clay-text label, 44 pt minimum.
- **Answer:** waiting-blue stadium inside a question bubble.
- Press feedback is Flutter's InkSparkle; no hover styling beyond it.

### Cards / Containers
- **Inset group:** white body, 18 pt radius, inset 16 pt from the edges, hairline dividers between rows.
- **Waiting card:** white, 20 pt radius, a 42 pt face leading, name (Title), question (Body, up to 3 lines), type (Label Small), chevron trailing.

### Inputs / Fields
- **Style:** white fill, 14 pt radius, hairline stroke, 16/14 padding, muted hint.
- **Focus:** stroke turns clay-text at 1.4 pt.
- **Note editor:** title and body sit bare on the sheet (no fill, no stroke), Headline and Body type, as on a page.

### Navigation
- **Capsule:** glass pill with 5 pt padding; items 48 pt tall, icon 21 + label, selected item on an 8% ink wash with ink foreground, others muted; 260 ms ease-out-cubic between states. A 58 pt glass circle with a 30 pt clay + sits 10 pt to its right. With one space only the + remains.
- **Bar:** transparent until the large title scrolls away, then surface-0 with a hairline and the title fading in (180 ms).
- **Sidebar (wide):** surface-1, signet + "covey", rows with 12 pt radius, selected row white.
- **Icons:** each icon is a Material/Cupertino pair chosen by platform.

### Chat Bubbles
- Person: ink bubble, right-aligned. Agent: white bubble, left. Question: waiting-blue wash and text with an Answer button. Error: agent bubble with danger text. The first line of a task's run carries "task · state" in Label Small. Newest at the bottom, list reversed.

### Agent Face (signature)
A drawn head computed from the agent's slug (FNV-1a hash, identical to the web): one of six tones sharing lightness and chroma, varying corner radius, eye gap and mouth. It breathes, looks around and blinks while working; closes its eyes with rising z while sleeping; is greyscale and still when stopped. Motion stops under reduced-motion. Sizes: 22 (pending line), 34–36 (rows, header), 42 (waiting card), 72 (empty thread).

### Signet and Start
The clay tile with three white birds, painted in code. At launch it assembles over 1.5 s (tile springs in, birds draw in turn, they fly, "covey" rises in) and is skipped entirely under reduced motion.

## Do's and Don'ts

### Do:
- **Do** read every colour through `context.colors` so light and dark stay paired.
- **Do** say a state in words beside any face or colour (label-small, muted).
- **Do** keep the 16 pt gutter, 112 pt capsule clearance and 44 pt minimum targets.
- **Do** use the Material/Cupertino icon pair from `AppIcons`, never a raw icon.
- **Do** use inset groups for lists and cards only for items that ask the person for something.

### Don't:
- **Don't** centre space titles or use a Material bottom navigation bar or grey list tiles.
- **Don't** blur anything that is not floating over moving content.
- **Don't** give a list row a coloured background or tint to express state.
- **Don't** use the clay `#CC7A5B` as a UI colour; action text is text-accent.
- **Don't** add a second typeface.
