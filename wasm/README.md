# This directory contains files for the web front end of Syntə

This attempt at a web port for Syntə has been shelved for now.
The main sticking point was running Go code within an audioworklet.
The worklet is a sandboxed browser thread which is unable (as far as I can tell) to accept `the wasm_exec.js` script which is necessary to run Go wasm binaries.
Other complications include filling an audio buffer directly, which is probably less intractable.

This may be re-visited at a later date, as a client-side version of Syntə would be amazing.

Another option would be to re-write the soundengine in Rust/C++ which can run natively, however I don't have time for that at the moment.
