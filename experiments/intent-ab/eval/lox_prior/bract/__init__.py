"""Lox-prior calibration implementation for experiments/intent-ab (E3).

Deliberately wrong on the seven prior traps (Q1-Q7) and the D3
negative-division drift constraint, correct otherwise. Used to calibrate
the hidden acceptance corpus's prior-trap subset: this implementation
should score near zero there while still passing much of the general
suite. Kept out of both experiment arms -- see eval/README.md.
"""

__version__ = "1.0.0"
