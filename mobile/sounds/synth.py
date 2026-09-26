"""The app's notification sounds (#381), synthesised here from sine waves:
no samples, so nothing to license, and anybody can change a sound and build
it again.

    cd mobile/sounds && python3 synth.py     # needs numpy and macOS afconvert

Three families — bot, flock ("schar"), glass — each with a sound per kind of
news: a question ends rising, an answer settles down, a finished task
resolves like a small major chord, a failure falls softly by a minor third.
All of them are meant to be heard many times a day: low, soft attacks, no
harsh overtones, a little room around them.

Writes covey-<family>-<kind>.caf next to this file (16-bit PCM in a CAF, the
container both iOS and macOS notifications take).
"""
import os
import subprocess
import wave

import numpy as np

SR = 44100
HERE = os.path.dirname(os.path.abspath(__file__))


def env(n, attack=0.012, release=0.4):
    t = np.arange(n) / SR
    return np.clip(t / attack, 0, 1) * np.exp(-t * 3.2 / release)


def phase(freq):
    return 2 * np.pi * np.cumsum(freq) / SR


def glide(points, n):
    """Pitch over a note: (position 0..1, Hz) points, glided on a log scale."""
    t = np.linspace(0, 1, n)
    ts, fs = zip(*points)
    return np.exp(np.interp(t, ts, np.log(fs)))


def room(x, mix=0.28):
    """A small Schroeder reverb: four combs, two allpasses."""
    out = np.zeros_like(x)
    for d, g in ((1116, 0.8), (1188, 0.78), (1277, 0.76), (1356, 0.74)):
        y = np.zeros_like(x)
        buf = np.zeros(d)
        j = 0
        for i, s in enumerate(x):
            o = buf[j]
            buf[j] = s + o * g
            y[i] = o
            j = (j + 1) % d
        out += y
    for d, g in ((556, 0.5), (441, 0.5)):
        y = np.zeros_like(out)
        buf = np.zeros(d)
        j = 0
        for i, s in enumerate(out):
            b = buf[j]
            buf[j] = s + b * g
            y[i] = b - s
            j = (j + 1) % d
        out = y
    return x * (1 - mix) + out * mix * 0.22


def render(notes, tail=0.6):
    """notes: (start s, signal). Mixed, given room, levelled, faded."""
    length = max(at + len(sig) / SR for at, sig in notes) + tail
    x = np.zeros(int(length * SR))
    for at, sig in notes:
        i = int(at * SR)
        x[i : i + len(sig)] += sig
    x = room(x)
    x = x / np.max(np.abs(x)) * 0.5  # about -6 dBFS: present, not loud
    fade = int(0.05 * SR)
    x[-fade:] *= np.linspace(1, 0, fade)
    return x


# --- bot: a small robot that hums, not one that beeps ---------------------


def hum(points, dur, vibrato=0.0):
    n = int(dur * SR)
    f = glide(points, n)
    if vibrato:
        f = f * (1 + vibrato * np.sin(2 * np.pi * 5.5 * np.arange(n) / SR))
    p = phase(f)
    s = np.sin(p) + 0.08 * np.sin(2 * p)
    return s * env(n, attack=0.02, release=dur * 0.9)


BOT = {
    "question": [(0.0, hum([(0, 700), (1, 740)], 0.16)), (0.17, hum([(0, 760), (0.3, 780), (1, 1100)], 0.34, 0.006))],
    "answer": [(0.0, hum([(0, 880), (1, 920)], 0.15)), (0.16, hum([(0, 900), (0.4, 880), (1, 660)], 0.34))],
    "result": [(0.0, hum([(0, 660), (1, 660)], 0.14)), (0.14, hum([(0, 830), (1, 830)], 0.14)),
               (0.28, hum([(0, 990), (0.5, 990), (1, 980)], 0.42, 0.004))],
    "error": [(0.0, hum([(0, 620), (1, 610)], 0.2)), (0.22, hum([(0, 540), (1, 510)], 0.42, 0.008))],
}

# --- schar: the flock — soft chirps, fewer and lower than birdsong --------


def chirp(f0, f1, dur=0.09):
    n = int(dur * SR)
    t = np.linspace(0, 1, n)
    p = phase(f0 * (f1 / f0) ** (t**0.6))
    return (np.sin(p) + 0.1 * np.sin(2 * p)) * env(n, attack=0.008, release=dur)


SCHAR = {
    "question": [(0.0, chirp(1050, 1250)), (0.14, chirp(1250, 1750, 0.13))],
    "answer": [(0.0, chirp(1400, 1550)), (0.13, chirp(1250, 1100, 0.12))],
    "result": [(0.0, chirp(1000, 1180)), (0.11, chirp(1180, 1400)), (0.22, chirp(1400, 1650, 0.12))],
    "error": [(0.0, chirp(1100, 1000, 0.1)), (0.16, chirp(950, 800, 0.14))],
}

# --- glas: glassy bells an octave lower than a phone's -------------------


def bell(f, dur=1.1):
    n = int(dur * SR)
    t = np.arange(n) / SR
    index = 1.1 * np.exp(-t * 7)
    s = np.sin(2 * np.pi * f * t + index * np.sin(2 * np.pi * f * 3.5 * t))
    return s * env(n, attack=0.004, release=0.55)


E5, G5, GS5, B5, C5 = 659.3, 784.0, 830.6, 987.8, 523.3
GLAS = {
    "question": [(0.0, bell(E5)), (0.16, 0.85 * bell(B5))],
    "answer": [(0.0, bell(B5)), (0.16, 0.85 * bell(E5))],
    "result": [(0.0, bell(E5)), (0.12, 0.85 * bell(GS5)), (0.24, 0.75 * bell(B5))],
    "error": [(0.0, bell(E5)), (0.2, 0.85 * bell(C5))],
}


def main():
    for family, kinds in (("bot", BOT), ("schar", SCHAR), ("glas", GLAS)):
        for kind, notes in kinds.items():
            name = f"covey-{family}-{kind}"
            wav = os.path.join(HERE, name + ".wav")
            x = render(notes)
            with wave.open(wav, "wb") as w:
                w.setnchannels(1)
                w.setsampwidth(2)
                w.setframerate(SR)
                w.writeframes((x * 32767).astype(np.int16).tobytes())
            subprocess.run(
                ["afconvert", "-f", "caff", "-d", "LEI16", wav, os.path.join(HERE, name + ".caf")], check=True
            )
            os.remove(wav)
            print(name)


if __name__ == "__main__":
    main()
