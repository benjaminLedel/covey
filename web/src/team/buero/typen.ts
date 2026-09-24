import type { Agent } from "../../api";

/* The types that plan, scene, life and component share. They stand on
 * their own so that the catalogue files and the scene do not have to import
 * the plan just to describe a room.
 *
 * Measures are CENTIMETRES. The building is computed like a house, and the
 * camera decides how large it stands on the stage — not the other way round.
 */

export type Punkt = { x: number; y: number };

/** A seat at a desk: the point the figure stands on, and the facing in
 *  radians; the furniture is rotated around it as a group. */
export type Sitz = { x: number; y: number; dreh: number };

export type Gemein = "besprechung" | "kueche" | "lounge";

/** What the office may know about a colleague: it comes from the data, always. */
export type Zustand = "schlaeft" | "arbeitet" | "frei" | "wartet" | "gestoppt";

/** A department with its people — what the component hands to the plan. */
export type Gruppe = { id: string; name: string; farbe: string; leute: Agent[] };

/** The names of the common rooms, translated by the component. */
export type Raumnamen = { besprechung: string; kueche: string; lounge: string };

export type Trennwand = { x: number; y0: number; y1: number; art: "glas" | "halb" };

/* A room in the plan.
 *   x, y, w, h        the room's inner measure, without walls
 *   id, name, farbe   department ("" for "no department") or common room;
 *                     farbe is a CSS hex like "#6d8c5a" or ""
 *   gem               set when the room belongs to nobody
 *   variante          lounges only: their running number in the house, so
 *                     that two lounges are not furnished alike
 *   leute, sitze      one seat per person, in the same order
 *   spalten, zeilen   the grid the room was measured by
 *   oben              lies above its corridor, the door is in the bottom wall
 *   tuerX, wand, ri   door centre, door wall, direction room → corridor (+1/−1)
 *   innen, aussen     waypoints inside and outside the door; flur, flurY: the corridor
 *   nr                room number on the floor, for the sign
 *   treff             meeting points in common rooms, where one goes to stand
 *   trennwand         large rooms only: a vertical wall whose gap is the way
 *                     through; no path to a seat crosses it
 *   hell              set by the scene: somebody works here
 */
export type Raum = {
  id: string;
  name: string;
  farbe: string;
  gem?: Gemein;
  variante?: number;
  leute: Agent[];
  sitze: Sitz[];
  x: number;
  y: number;
  w: number;
  h: number;
  spalten: number;
  zeilen: number;
  oben: boolean;
  flur: number;
  flurY: number;
  nr: number;
  tuerX: number;
  wand: number;
  ri: 1 | -1;
  innen: Punkt;
  aussen: Punkt;
  treff: Punkt[];
  trennwand?: Trennwand;
  hell?: boolean;
};

export type Flur = { y: number; h: number; mitte: number };

export type Plan = {
  breite: number;
  hoehe: number;
  raeume: Raum[];
  flure: Flur[];
  /** The cross corridor on the left that joins every corridor to the entrance. */
  quer: { x: number; w: number; mitte: number };
  /** The front desk at the head of the cross corridor and the queue before it. */
  tresen: { y: number; plaetze: Punkt[] };
  /** The stairwell at the END of the cross corridor, the same spot on every
   *  floor: where the way up begins and where it ends. Drawn and used only
   *  when the house has more than one floor. */
  treppe: Punkt;
};

/* The house: one plan per floor.
 *
 * A storey carries about forty-four seats. Beyond that the plan grows in
 * depth until it is a tower seen from above, with rooms so small that a face
 * covers the desk. Departments are flat, so there is only one rule for the
 * floors: FILL BY HEADCOUNT, in the order of the org chart, never splitting
 * a department; one larger than the measure gets its own storey. */
export type Etage = { nr: number; gruppen: Gruppe[]; plan: Plan };
export type Haus = { etagen: Etage[] };
