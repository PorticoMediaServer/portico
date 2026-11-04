#!/usr/bin/env python3
"""Trace only process events; private trace is deleted with fixture state."""
import os
os.execvp('strace',['strace','-f','-ttt','-T','-e','trace=process','-o',os.environ['SCANNER_PROCESS_TRACE'],'--',os.environ['SCANNER_REAL_BINARY']])
