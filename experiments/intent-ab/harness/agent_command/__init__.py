"""Bonsai executor wrapper (E4) for the intent A/B experiment.

A minimal `agent_command` CLI: reads a Cloche-assembled prompt on stdin,
drives bonsai-8b-16k through host Ollama's OpenAI-compatible endpoint,
applies the file edits the model proposes, and echoes the step's
CLOCHE_RESULT marker to stdout. See ../README.md for the full contract and
the bonsai-specific handling rules baked in here.
"""
