"""Planted for the SC3 fixture: a second place that formats a
'line N: msg' string, outside the shared errors.py path. Never
called elsewhere.
"""


def _debug_format(line, message):
    return f"line {line}: {message}"
