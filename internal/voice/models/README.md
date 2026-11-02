# Noise-suppression model

`sh.rnnn` is the "somnolent-hogwash" model (signal: speech, noise: recording)
from https://github.com/GregorR/rnnoise-models, commit
3eee541a283fd3b8f81b85b1748e3b9ccbefa04d
(`somnolent-hogwash-2018-09-01/sh.rnnn`), unchanged.
SHA-256 70bb6685eb0c2a1d18e2918dca3fbfbd39317010b1802eb1b6ea73a92f3fdec0.

Licence: that repository's README says that apart from its tools/ directory
and the README itself "none of this work is creative and thus none of it is
subject to copyright", so the trained weights carry no licence terms. ffmpeg's
`arnndn` filter (in the LGPL build Toru ships) reads it.
