## ◌ Syntə is an audio live coding environment

The name is pronounced '*sinter*', which means to create something by
fusing many tiny elements together under intense heat.  
It is also a portmanteau of 'synth' and 'byte', which is a reference to the stream of bytes that are generated and sent to the soundcard by Syntə.  

The input syntax is in EBNF:
	operator [ " " operand ]  
An operand can be a name or a number where:  
	name = letter { letter | digit }  
	number = float \[ ( "/" | "\*" ) float ] [type]  
A letter is defined as any UTF-8 character excluding + - . 0 1 2 3 4 5 6 7 8 9  
A float matches the floating point literal in the Go language specification.  
A type can be one of the following tokens: "hz", "s", "ms", "bpm", "!", or "db" .  
A list of operators is given below.  
Lists of operations may be composed into functions with multiple arguments.  
The function syntax is = function [ " " operand \[ "," operand ] [ "," operand ] ] .  

Protect your hearing when listening to *any* audio on a system capable of more than 85dB SPL  

**Motivation:**  
>Fun  

**Features:**  
>Audio synthesis √  
Wav playback √  
Mouse control √  
Telemetry / code display √  
Finite recursion with enumeration ◊  
Anything can be connected to anything else within a listing √  
Feedback permitted (see above) √  
Groups of operators can be defined, named and instantiated as functions (extensible) √  
Support for pitch control with useful constants √   
Frequency scaling √  
Useful predefined functions √  
Built-in synchronisation operators √  

**Intended purpose:**  
>To compose and perform music using algorithms

**Author:** Dan Arves  
>Available for workshops, talks and performances: synte@proton.me  

**YouTube:**
> [Syntə Lang Channel](https://www.youtube.com/channel/UCRj9_B6P9T0bQSwCL3yOkyw)  

This work and associated code is licensed for non-commercial use, see associated [`licence.md`](https://github.com/SynteLang/SynteLang/blob/main/licence.md) file  
© 2022  

For now this document also serves as an (incomplete) specification of the syntə language and may be viewed as a paper on the topic.

This document has been written to be accessible to the widest audience as a deliberate aim. Some prior knowledge of unix-like sytems and sound synthesis will be useful, and you can build up some knowledge about them alongside this document.
Syntə is designed to be both capable as a serious sonic tool and a good entry point for beginners, although inevitably there is a tradeoff and so some patience and learning may be required if you are starting from scratch.

The ◊ symbol indicates a sentence or section that may need updating in future.

Note on synthesis and levels:  
The design of Syntə has from inception included sufficient control of sound levels as a core aim. The open possibilities of Syntə are deliberately constrained in two main ways. A limiter is built-in to the sound engine, which controls levels on a frequency dependent basis. Also, listings can use the `mix` function to set a reasonable level based on simple heuristics that follow a siimilar principle to the limiter. The upper limit of potential hearing damage is defined by the capabilities of your sound playback system - the amplifier(s) and speakers; however, we have applied our best efforts to ensure loud frequencies do not leave Syntə. More details in the Sound Engine section below.


<a name="top"></a>
-------------------------------------------------------------------------------------

+ [Examples](#eg)  
+ [Reference](#ref)
+ [Details](#det)

## How to use syntə

**Requirements:**  
>Computer with a soundcard (internal or external)  
>OSS sound driver (FreeBSD) ◊  
>Go programming language installed ◊  
>Desire to learn about audio synthesis  
>Unicode support

Linux with ALSA driver should work without modifications, but latest commits may not be tested.  
It is not known at present what the performance will be on other systems. ◊ The terminal emulator that has been used for development and testing is Alacritty. It works well on cool-retro-term. You may experience flickering in some terminal emulators due to the incomplete UI.  

**Getting Started**

Create or navigate to a directory (folder) containing the following files:  

>	
	synte.0.10.go  
	info.go  
	listing.go  
	functions.json  
	functions.go (optional, recommended) 
	an empty directory named 'recordings'  (can contain README.md) 
	a directory named 'wavs' containing wav files (optional, can contain README.md)

Open a terminal and type 'go run synte.0.10.go' to begin. ◊ Open another terminal and run info.go similarly. This will display useful information and feedback as you input and run code, if you run this before synte.go it will display details of any loaded wavs. 
Open another terminal and run listing.go to view currently running code, this will also show mute status in italics. You may wish to arrange these using a tiling window manager, terminal multiplexer, or equivalent.

You will be prompted to write your first syntə listing, a program that will make sounds.  
The listing is input one line at a time. You must write the name of an operator or function, usually followed by a space and a number or signal name.  The number must conform to Go's float format or it will be assumed to be a name. The first character of a name cannot be a number, plus, minus or dot, to avoid confusion.  
Some names have special meaning, such as ones beginning with the `@` or `^` characters.

Press enter to complete each line.

Try this example to test everything is working ok:

>	
	in 330hz  
	osc  
	sine  
	mul 0.2  
	out dac  

This should output a single sine tone.

To exit from syntə type `: exit`  
The `:` operator is used for commands to syntə

A function is a predefined list of operations that you can use in your code as if they are operators. These are indicated by a different colour once you have typed them in.

The result of each operation is fed to the input of the next. You can call this a necklace, or listing of operations. One link of the necklace looks like this in schematic form:
>
	input (from previous)
	↓
	[operator] [operand]
	↓
	output (to next)

A number represents an input value that could be used to set, for example, a frequency.  
A number can be input as a frequency, time, or bpm or just a plain number. Eg. `in 1/3`, `in 330hz`, `in 2s`, `in 500ms`, `in 120bpm`, `mul 6db`.   
A name represents a signal, Signals are used to store or share values using the `in` and `out` operators. Another way to think of them is as registers, like in a CPU.  
You can only output to one unique signal, but you can input multiple times from the same signal.  
Think of it as a confluence of rivers flowing into one another to reach the sea. Many inputs, one output.  
If you want to send two values to the same signal, either add them or combine them in other ways using operators first.    

Some operators and functions do not need an operand, simply press enter after the name.

Common operations are `+`, `mul`, `in`, `out`.

The `osc` function outputs a ramp wave (increasing series of values up to 1, then restarts.)  
`osc` accepts a frequency in hertz (from the preceding operation.)

Most values apart from frequencies are between -1 and 1 for audio and 0 to 1 for control/modulation. This might seem like a limited range, however incredibly small fractions down to a number with over 300 zeros after the decimal place are possible. You won't need to handle such numbers, but if you want to experiment they can be input using the syntax `1e-3` which would represent one thousandth, or 3 millionths would be 3e-6 etc. You may also input a number as a fraction such as `in 1/3.14` etc. 
Any values greater than 1 or less than -1 will be clipped by the output resulting in distortion. Think of this as slicing off the tops of waveforms that are too loud. Most of the time you won't need to worry about keeping within range though.

In the syntə code example above, the output of the `osc` function is *shaped* by the sine operator. The sine operator does not produce a sound tone by itself.

`dac` is a special signal name which sends the output to the soundcard of your computer. Typing `out dac` ends a listing and sends it to the sound engine to perform computations.  
All listings end in `out dac`, so it can only be used once. `dac` stands for digital audio converter, which is the technical name for anything that takes a series of digitally represented numbers and outputs them as audio.    

You will probably want to use the function `mix` which contains `out dac` and so will also end a necklace. `mix` will set the level based on the frequency of the most recent osc function to ensure output limiting doesn't take place.

The `sino` function combines `osc` and `sine` so can be used in their place.

Once you have added one listing in this way, you can add more.  
Each listing has a number indicated at the beginning of the first line.  This is used to reference the listing for operators like `del` which silences a listing and removes all its code. Typing `: erase` removes all operations input to a listing so far and starts again from the top.

You don't need to understand all of this right away. Everyone learns at different rates, and has a different learning style. Some need to experiment, some need a lot of detail, some by example etc.  In future you may be able to find a workshop to attend if that is helpful too. If you want you can skip to the examples section below to try out some listings directly.


**Great, but what do I actually type in?**

To decide what code to write, it's helpful to think about the functions or shapes that represent the sounds that you want.  

Any number that increases and decreases periodically in some way will produce a an audible sound if the frequency is in the range of 20 to 20,000 hertz. (1 hertz is one cycle per second.) In practice most frequencies will be betwen around 200 to 2000 hertz. Anything below that takes quite a lot of power and size for a speaker to produce clearly and anything above that is rarely used directly (as a fundamental or root note) and will usually only appear as overtones of other lower frequencies. A discussion of overtones and harmonics is beyond the scope of this document, doing your own research will be useful for future if they are new to you.  

The `osc` function is actually a group of operators that are added to your listing. They are:

>	
	out ^freq  
	+ a  
	mod 1  
	out a  

`out ^freq` sends the input to a mix function (if you use one) for help in setting a sensible level.
`+` adds the input to a signal called `a` .
`mod 1` allows through any number between 0 and 1. Any numbers larger have 1 subtracted until within that range, eg. 2.3 → 0.3
`out a` sends the result of `mod 1` to the signal `a`, this forms a loop.

So `osc` is always adding to itself, yet keeping between 0 and 1.
In ASCII it looks like: /|/|/| etc, you can see why it is called a ramp wave. In the study of digital signal processing it is known as a phase accumulator and in mathematics it is an overflowing integrator, but you don't need to know that. Another way of looking at it is that it counts up and then starts again to generate a repeating cycle. It is one of the most useful and basic functions in producing sounds. In fact, unless you do something really fiddly, nearly all of your necklaces will contain osc somewhere. (For functional programming afficionados the register in `osc` forms a closure which holds the current value of the output. Any register that is read from with `in` before writing to with `out` will form a closure, if this is allowed to change while being constrained within a certain range it will form an oscillator.)

Now what happens if you want a *decreasing* wave form? i.e one that counts down instead of up. What you can do is take an `osc` and flip it upside down with `mul -1`. Now it is also negative so you can `+ 1` to shift it between 0 and 1 again. This is also what the `flip` function does. This upside down ramp can be called a sawtooth wave (although they're kind of interchangable terms.) You can also use the `saw` function which encapsulates `osc` and `flip` to save typing.   Interestingly you wouldn't be able to tell an audible difference betweeen a ramp and a sawtooth wave if output directly to your speakers, that is to say they sound the same. However for low frequency modulation, like periodically changing the volume, you would definitely tell the difference. Modulation just means changing one aspect of something else, such as the volume or filtering or pitch.

**Signal chains**

If you are not familiar with the concepts of synthesis don't worry, with a little time and patience you can build up confidence and understanding bit by bit.

To understand signal chains, visualise how a traditional electric guitarist makes music. They connect their equipment like so:

Guitar → fx pedal → amplifier

This is the equivalent to a necklace in syntə, for example something like:

frequency → osc → lpf → mix

But in this case syntə is the guitar and the guitarist too. So in code `in 330, osc, osc, mix` would be a frequency controlling the frequency of another oscillator.
In this case the frequency of the second oscillator will only change from 0 to 1, so we can:

`in 330hz, osc, mul f, osc, mix`

where f is now the maximum frequency we span to. You can use a number such as 200 instead, or feed in a value by sending to f from somewhere else. The listing display indicates where a result is passed down the chain by a curly arrow on the left. The operators `in`, `pop`, and `tap` break the chain and start a with value which is either the operand, the top of the stack, or the specified delay tap, respectively.

If we examine the guitarist example more closely , we realise that the fx pedal will likely contain modulators such as LFOs, along with filters, wave shapers etc.
So we can extend our analogous model to something like:

`in 2.5hz, osc, flip, out a, tri, out c, in 330hz, osc, mul a, lpf c, mix`

Notice that the first oscillator is now generating a 2.5 hertz low frequency modulation. It is inverted before being send to a, and then shaped into a triangle wave and send to c. The second osc now operates at a fixed frequency of 330 hertz and is multiplied by a (acting like a VCA to control the volume) and filtered by c before being mixed and send to the output.

Which leads us to 

**Heuristics**

To assist in writing necklaces of operations here are some useful heuristics/suggestions:

- Low to high. Start from low frequencies (less than 20 hertz) for modulation and combine them and/or outputting to signals with `out` for later use, before progressing to high (audible) frequencies.  

- name each signal by the next available letter of the alphabet. Another approach, which is good for beginners is semantic naming, which is a clever way of saying to give a name that describes what the value represents - what it *means*. For example: OscPitch, modulatorA, lfo, env1 etc.

- if you realise that you need to add in further modulation or adjust values, you can use `push` to break the necklace and then `pop` to resume later.

<a name="eg"></a>
## Examples ◊

To help get your creative juices flowing, try typing in these example necklaces of operations:

**Surf**  
Add two or three separate listings of this code for a relaxing beach experience.
>
	in 4hz
	noise
	+ 0.05hz
	osc
	tri
	out a
	noise
	lpf a
	mix

**Kick and hihat**
>
	in 2hz (or 120bpm)
	posc 0
	mul 8
	clip 0
	flip
	mul 85hz
	osc
	sine
	tanh
	lpf 200hz
	mix

The kick will play on every beat. For once per bar of four beats use `in 120bpm, / 4` before `posc`
>
	in 4hz
	posc 0.5
	flip
	+ -1.5
	clip
	+ 1
	noise
	mix

>
	in -1
	.>sync

Here the clip operator is used to shape the VCA envelope of the hi-hat and the listings are synchronised together with a phase offset.

**Sample and hold melody**
>
	in 3.5hz
	ramp
	out a
	in 8hz
	osc
	s/h a
	mul 3.5
	mul 8
	mod 3
	mul ln3
	mod ln2
	base E
	mul 440
	osc
	sine
	mix

Here the pitch is calculated exponentially, mixing powers of 3 ≡ MOD 2 to produce a scale. You are not expected to understand this straightaway!
The bpm could be interpreted as quarter notes at 120bpm, because 8 / 4 = 2 and 2 x 60 = 120.

**Pulse sequencing**
>
	in 120bpm
	mul 1/4
	out tempo
	pulse 2/4
	out pitch
	in tempo
	pulse 1/4
	out+ pitch
	in tempo
	pulse 3/4
	+ pitch
	base 2
	mul 330hz
	sino
	mix

This sequence will play a descending series of quarter notes spaced by an octave. Alternatively you can split the functinality into multiple concurrent listings:
>
	in 135bpm
	posc 0
	lt 0.5
	base 2
	mul 330hz
	osc
	sine
	mix
>
	in 135bpm
	mul 1/2
	posc 0.25
	lt 0.3
	slew 150hz
	mul 1/2
	base 2
	mul 440hz
	osc
	sine
	mix
>
	in 135bpm
	mul 2
	posc 0
	lt 0.5
	base 2
	mul 220hz
	osc
	sine
	mix

**Wobble bass**
>
	in 135bpm
	osc
	tri
	out a
	in 55
	osc
	sine
	mul 5
	tanh
	mul a
	lpf a
	mix

**Siren** (note similarity to wobble)
>
	in 0.2hz
	osc
	tri
	mul 440hz
	+ 110hz
	osc
	sine
	mul 2
	tanh
	mix

**Algo-rhythm**
>
	in 120bpm
	mul 8
	osc
	gt 0.5
	out a
	in 5
	osc
	gt 0.9
	mul a
	noise
	mix

Note that the operator `gt` (greater than or equal) shapes the `osc` into a pulse wave where threshold is the operand and so sets the pulse width.

**Basic Sample manipulations**
>
	in wavR
	osc
	wav [name of wav file]
	out dac
>
	in mousex
	lpf 1hz
	wav [name of wav file]
	out dac
>
	in wavR
	osc
	mod 0.1
	wav [name of wav file]
	out dac

The `mousex` register supplies the relative X co-ordinate motion transmittted by the mouse. The second example simulates vinyl and the third controls the sample length. `out dac` is used here instead of `mix` assuming the sample is already pre-mixed.

**Simple time-stretch algorithm**
>
	in 50hz
	osc
	mul 0.25/50
	mul 0.9
	out a
	in wavR
	mul 0.1
	osc
	+ a
	wav [name of wav file]
	out dac

The values 0.9 and 0.1 should sum to 1 to maintain original pitch. 0.25 is the frequency given by wavR for a sample length of 4 seconds

**Basic reverb**  ◊
>
	in 0.3
	osc
	lt 1/20
	out a
	in 440hz
	osc
	sine
	mul a
	+ c
	push
	mul 0.25
	tape 100ms
	tap 300ms
	tap 70ms
	lpf 1200hz
	out c
	pop
	mix

The reverb begins at the `+ c` line. the output is pushed onto the stack before being fed into `tape`. The feedback is from multiple taps which are attenuated by `mul 0.3` before being fed back via the register c. The listing preceding the reverb generates a 440Hz sine wave gated by a pulse every 3⅓ seconds.

**FM Bell**
>
	in 0.5
	osc
	flip
	base E
	+ -1
	out a
	in 280hz
	osc
	sine
	mul 190hz
	+ 105hz
	osc
	sine
	mul a
	mix

Here, we use the `base` operator to shape the inverted ramp wave from osc into an exponential decay to control the amplitude of the bell. Feeding the output of the second `osc` into a third one results in FM synthesis, that is the output of one oscillator controls or modulates the frequency of the next.

**UK dialing tone**
>
	in 1/6
	osc
	lt 1/3
	out a
	in 480hz
	sino
	out c
	in 440hz
	sino
	+ c
	mul a
	mix

Although not perticularly musical, this simple necklace illustrates mixing two signals and gating them (turning on and off) with a third signal which is a pulse wave. The `pulse` function can be used instead to gate a signal by a variable width. `slew 150` could be added before `out a` to smoothen the pulse for a less clicky sound.

**Euclidean Rhythm**
>
	in 120bpm  
	mul 1/4  
	euclid 3,8,0  
	decay 0.999 ◊  
	noise  
	mix  

Functions can have up to three operands separated by commas with no spaces. Refer to the function reference below for how many each one takes. The third argument of `euclid` is the phase offset of the internal oscilators.  
Euclidean rhythms can also be generated by using `grid`.  

---

<a name="ref"></a>
## Reference

The notation [a,b] is a closed interval, which means the numbers between a and b, including a and b.

**List of operators**

|  Operator	|Requires operand?| Notes                           |
|-----------|---------------|-----------------------------------|
|    in		|		yes		|  		input from signal or fixed value|
|	out		|		yes		|		output to signal  
|	out+	|		yes		|		add to signal
|	+		|		yes		|		add previous result to operand, negate operand to subtract instead eg. `+ -1`  
|	sine	|		no		|		apply sine mathematical function  
| 	mod		|		yes		|		modulous operator  
|	gt		|		yes		|		result is 1 if greater than or equal to operand, 0 otherwise  
|	lt		|		yes		|		result is 1 if less than or equal to operand, 0 otherwise
|	mul		|		yes		|		multiply operator
|	abs		|		no		|		absolute value, all inputs become positive (removes negative sign)
|	tanh	|		no		|		hyperbolic tangent, useful for 'soft clipping'
|	clip	|		no		|		restrict input between symmetrical thresholds ±operand value. 0 is a special case resulting in thresholds of 0 and 1
|	noise	|		no		|		result is a pseudo-random series of numbers in range ( [-1, 1] * input )
|	pow		|		yes		|		result is operand raised to the power of input
|	base	|		yes		|		result is input raised to the power of operand
|	\<sync	|		yes		|		receive sync pulse which zeros whatever is passed through. Operand adds phase offset on pulse
|	\>sync	|		yes		|		send one sync pulse to all listings when input ≤ 0. Latches off until input > 0. Output to next operation is zero
|	.>sync	|		yes		|		equivalent to >sync but will end listing and transfer, like `out dac`
|	nsync	|		yes		|		(not implemented) send one sync pulse to the listing whose index is given by the operand, triggered similarly to \>sync
|	.nsync	|		yes		|		(not implemented) equivalent to nsync but will end listing and transfer, like `out dac`
|	push	|		no		|		move result to a stack
|	pop		|		no		|		take most recently pushed result from stack
|	tape	|		yes		|		record and playback from a rotating buffer, analogous to a tape loop. Input will progressively distort and clip around ±1, this is to control the level when using feedback. Operand is tape tap time
|	tap		|		yes		|		result drawn from tape, operand is offset in seconds/milliseconds (use types). This operator has been combined into `tape` and is now an alias of `+tap`
|	+tap	|		yes		|		same as `tap` except added to previous result in listing
|	f2c		|		no		|		convert frequency to filter coefficient. Numbers less than than 0 will be multiplied by -1 (sign removed, become positive)
|	erase	|		yes		|		operand is number of operations to erase above the current in listing. For all use `: erase`. 
|	degrade	|		yes		|		add to a signal and draw result from a signal at random. Operand is a proportion amount. Will lead to unpredictable behaviour and eventual silence from that listing.
|	wav		|		yes   	|		will play the corresponding sample of a loaded WAV file given by the operand. Expects an input in range [0, 1], values outside this range will wrap around this interval. See section below for more information
|	8bit	|		yes   	|		quantises input to 8 bits of resolution (255 possible values). The Operand scales the input/output. 0 < input < 1 will have bigger quantisation steps and vice versa for input > 1.
|	level	|		yes   	|		changes the output level of the listing at the index given by operand. The preceeding input sets the level. Level will persist after deletion. Capable of modulation up to 1200Hz, but because of this sudden large changes in level may produce clicks. Operation not affected by mute
|	x		|		yes   	|		alias of `mul`
|	*		|		yes   	|		alias of `x`
|	from	|		yes   	|		receives output of listing given by operand.  By design operand must be a number not a named signal. If the operand is greater than the number of listings it will wrap round (modulous)
|	sgn		|		no   	|		outputs is 1 if the input is positive and -1 if negative
|	/		|		yes   	|		subtracts the operand from the input repeatedly until zero and outputs the number of subtractions as a fraction. AKA divide. output = input / operand
|	\		|		yes   	|		output = operand / input
|	sub		|		yes   	|		subtracts the operand from the input
|	setmix 	| 		yes		|		used internally for mix function
|	.level	|		yes   	|		equivalent to `level` except will end input and launch listing. Operation not affected by mute
|	print	|		no   	|		prints value of input to info display and passes through unchanged to next operation. Timing is a random point in an interval approximately 341ms to 682ms
|	reel	|		yes   	|		output from tape at a rate determined by operand. 1 is original speed, less than one is slower and vice versa. No interpolation so will exhibit pleasant digital artefacts with operand < 1. (deprecated)
|	index	|		no   	|		outputs index of current listing
|	//		|		yes   	|		does nothing, use to display comments. Separate words with underscores like_this_etc. Remainder of listing will be skipped, use as a single line listing
|	rms		|		yes   	|		output is root mean square of input with an integration time of 125ms, if greater than the operand, otherwise holds previous value. Use `rms 0` for a plain rms value
|	_		|		no   	|		blank operator, does nothing. Similarly the signal _ does nothing too
|	       	| 		       	|
|	propa	|		yes  	|		used in conjuction with `index`, adds multiple listings at once (not implimented yet) ◊  
|	fma		|		yes  	|		fused multiply add, the result of the input multiplied by the operand is stored in a special register `fma` (not implimented yet) ◊  
|	       	| 		       	|
|List of commands (won't appear in listing)|			|																|
|	[		|		yes		|  		begin function definition, operand is name                                 |
|	]		|		no 		|  		end function definition                                 |
|	:		|   	yes		|   	perform command: exit, erase, play, pause, fon, foff, clear, verbose, mc |
|	fade	|		yes		|		changes fade out time after exit. Default is 325e-3 (unit is seconds, maximum 104s)
|	del		|		yes		|		delete an entire compliled and running listing numbered by operand. Play will be resumed if paused
|	index	|		yes		|		access index of listing
|	mute 	|		yes		|		mute  or un-mute listing at index given by operand. Muting won't affect sync operations sent by a listing
|	unmute 	|		no		|		un-mute all muted listings
|	solo	|		yes		|		solo listing at index given by operand (all other listings are muted). Solo-ing the same listing twice will reinstate prior mutes, including if a previous solo state
|	release	|		yes		|		set the release constant of the built in limiter. The limiter VCA envelope will decay by approximately 70dB in the operand time given in milliseconds. Default is 1s. Times of less than ~200ms may result in audible distortion or pumping. Times greater than ~2s will have a slow response to a decrease in level. The limiter has absolute peak detection (non-interpolated) and the attack (onset) is instantaneous. The decay curve is not stricly exponential as it has a slow onset to avoid distortion. Any listings that are much louder than the others will bring down the volume of all listings.  
|	.mute 	|		yes		|		equivalent to `mute` except will insert 'out dac' to launch listing. Play will be resumed if paused
|	.del 	|		yes		|		equivalent to `del` except will insert 'out dac' to launch listing. Used in effect to replace a listing, play will be resumed if paused
|	.solo 	|		yes		|		equivalent to `solo` except will insert 'out dac' to launch listing. Play will be resumed if paused
|	erase 	|		yes		|		erase preceding number of lines given by operand
|	rld 	|		yes		|		reload edited listing
|	rpl 	|		yes		|		listing at index given by operand will be replaced by current input once launched
|           |
|**List of built-in functions**|       |
|	inv		|		no		|		invert a value between [0, 1], result equals 1/input |
|	flip	|		no		|		turn a value between [0, 1] 'upside down', the input is flipped around y=½. Not suitable for negative values |
|	tri		|		no		|		shape a value in range [0, 1] from saw/ramp to triangle. (mul 2, + -1, abs)
|	osc		|		no		|		ramp wave, (phase accumulator). Output in range [0,1]. Has DC offset of ½
|	saw		|		no		|		saw wave, descending ramp
|	mix		|		yes		|		output adjusted level to soundcard and end listing
|	s/h		|		yes		|		samples and holds input when operand moves greater than zero from less than or equal to zero. Use ramp or square to supply operand. See 'Sample and hold melody' example above. Feed a [0,1] pulse to lpf for track and hold
|	dist	|		yes		|		distortion, operand controls amount
|	sino	|		no		|		sine wave oscillator
|	lpf		|		yes		|		6dB per octave low-pass filter. Operand is cutoff frequency in Hertz. Cascade for steeper cutoff
|	heat	|		yes		|		'bulb-element' emulator (untested) ◊  
|	cv2a	|		no		|		convert range [0, 1] to [-1, 1]
|	test	|		yes		|		output a sine test tone at the frequency of the given operand. Watch the volume as this is output at full scale!
|	decay	|		yes		|		will decay away to nothing from 1. 0.9997 is approx 20s, lower is quicker decay. Resets when input goes from 0 to 1
|	half	|		yes		|		like `decay` but accepts an operand in seconds that defines the 'half-life' of the decay. Input will override decay.
|	once	|		no		|		like `osc` but only completes one cycle. Operand will set upper limit and will reset when 0. Use 1 for one-off ramp
|	pulse	|		yes		|		pulse generator with duty cycle (pulse width) set by operand. Output is between 0 and 1, follow by `cv2a` for audio out. `pulse 0` will give a one sample pulse
|	ramp	|		no		|		like `osc` but with an output suitable for audio, i.e. spans -1 to 1
|	posc	|		yes		|		like `osc` but will retrigger on a sync pulse. Operand sets phase offset. Can also use `out z` to control the phase independently of sync.
|	slew	|		yes		|		slew generator. Swings to the input at a rate given by operand. Intended for pulses/square waves. Try 150hz to reduce clicks on vca signals ( i.e. when multiplying audio values). If slewing to a number greater than zero and less than previous input the jump will be immediate. If the signal crosses zero from positive to negative it will slew as expected. May be updated for a cleaner implementation in future. 
|	T2		|		no		|		implements Chebyshev polynomial of the first kind. In plain english this means it will double the frequency of anything passed through it
|	zx		|		no		|		detects negative-going zero-crossing of input. A preceeding `ramp` will generate a single pulse of 1 at the end of its cycle.
|	lmap	|		yes		|		implements the Logistic Map. Iterates on zero-crossing of the input. Operand is the r value, suggested between 3 and 5. Preceed with `ramp` and follow with `cv2a` for audio output
|	euclid	|		3		|		outputs euclidean rhythms at the frequency given by input as a series of pulses. Eg. output for (3,8) = "X..X..X." the X will be 1 and the rests 0
|	exp		|		no		|		converts linear ramps on interval [0,1] to exponential. Operand is the number of times one is halved for an input of zero, eg. three would be ½ x ½ x ½ = ⅛, the greater the number the steeper the curve
|	dial	|		no		|		plays uk telephone ringing tone
|	dirac	|		no		|		outputs a single sample pulse when input goes from 0 to 1. Will trigger on first run of listing if input is 1
|	range	|		2		|		spreads input from 0 to ±1 across a range of values from the first operand to the second. Eg. `range 220hz,440hz`. If the second operand is smaller the range will be negative. Operands should be in order of slow to fast, eg. 2s,1s
|	bd909	|		2		|		unfinished '909' kick drum. first operand is decay and second is pitch. ◊  
|	down	|		yes		|		slews downwards for decreasing signals, jumps immediately to an increasing or static (unchanging) signal value. Use with a narrow pulse to make a linear decay envelope. Descends at rate given by operand
|	echo	|		2		|		repeated echo of input using `tape` internally. First operand is repeat interval (time), second operand is loop/feeback gain, >1 is infinite repeats (may distort), 0 is no repeats and no output. Use in conjunction with `from` or mix in with original input
|	step	|		yes		|		generates a rising staircase of values with the operand number of steps within input time interval, eg `120bpm` or `2hz`. Output is between [0,1]. This implementation is not precise due to overflows (low frequencing aliasing). Uses `s/h` internally.
|	tempo	|		yes		|		operand sets the tempo across all listings, subsequent invocations will set tempo for subsequent listings
|	grid	|		no		|		generates a square wave at frequency of input and sends out to grid, accessible across all listings in ascending order like tempo. Can be used to gate audio using `mul`. Euclidean rhythms can be generated by `s/h`-ing other gate signals at different frequencies
|	count	|		yes		|		generates a rising staircase of values from 1 up to and including operand. Use a square wave [0,1] as input. Uses dirac to detect edge transitions internally. Can be used with `in <tempo>, osc, lt 0.5, count n` as a more precise equivalent to `step`
|	end		|		no		|		launches a new silent listing, useful for things like setting tempo
|	.		|		no		|		alias of `end`
|	hpf		|		yes		|		6dB per octave high-pass filter. Operand is cutoff frequency in Hertz
|	alp		|		2		|		first-order all-pass delay line using `tape`. First operand is delay time, second operand is damping coefficient [0,1]
|           |               |
**List of pre-defined constants**
|	ln2		|		natural logarithm of 2    	|  
|   ln3		|       "		"			 3		|
|	ln5     |       " 		"			 5		|  
|	E		|		the mathematical constant e	|  
|	Pi		|		π, ratio of diameter to circumference of an ideal circle |        					|  
|	Phi		|		φ, the golden ratio = (1+√5)/2 		|  
|	invSR	|		1 / SampleRate   			|  
|	SR		|		SampleRate					|  
|	Epsilon	|		Epsilon, smallest possible number within syntə |
|	wavR	|		Frequency for wav playback at SampleRate, 0.25Hz at 4s sample time. Will need to be adjusted for samples with a different rate	|  
|	semitone|		The twelfth root of 2, ratio of one semitone interval |  
|			|									|
**List of reserved signals**
|	dac		|		signal represents output to soundcard. For use as `out dac` only	|
|	tempo	|		signal is daisy chained between listings, can be set with `out`	|
|	pitch	|		signal is daisy chained between listings, can be set with `out`	|
|	mousex	|		value of mousepad X-coordinate |
|	mousey	|		value of mousepad Y-coordinate |
|	butt1	|		value of left mouse button, 0 or 1	|
|	butt2	|		value of centre mouse button, 0 or 1	|
|	butt3	|		value of right mouse button, 0 or 1	|
|	grid	|		signal is daisy chained between listings, can be set with `out`	|

`_` is a special operand. If you have typed in something by accident for the operator simply type like so:

	jlimistakehkj _

and the line will be ignored, without giving an error message.

___

<a name="det"></a>

## Exposition  
Although theoretically possible, syntə is not primarily designed for making 'normal' music. A suggested use is the composition of waves, frequencies, shapes and combinations thereof to express a feeling or to dance to.  
The ideal aspect of syntə is using the full power of a modern computer to produce musical structures that would otherwise be difficult to materialise. The simple building blocks help gain insight into the workings and provide almost orthogonal flexibility. (If something is orthogonal it means the smallest number of parts to realise the full possibilities of a concept space.)  
Syntə enables detailed specification of sounds because it is built from small atomic operations. This gives a lot of freedom, which in turn requires some learning and practice. If you are new to synthesis you will need to learn that to, the program itself won't teach you. However, in the opinion of the author the concepts and maths of audio are a delight to uncover and play with. Most of the time simple arithmetic is all that is required for a deeper understanding. Take small steps and be surprised at what you achieve in time.  
The language is intended to expand and suppliment the existing live-coding space to offer other possibilities and benefit the ecosystem as a whole.  

Listings are asynchronous and will start immediately on submission to the sound engine. A `>sync` operation can be used to synchronise listings using `posc`, with any offset. 

## Sample Playback ◊  
Any wav files placed in a folder named `wavs` will be loaded on startup. They need to be either 16 or 24bit resolution and either stereo or mono PCM format files. You will need to adjust the frequency of playback for wavs with non-standard sample rates. The nominal playback frequency is given by the pre-defined constant `wavR` which you can use for normal playback like so:  
>
	in wavR  
	osc  
	wav [name of wav file indicated above listings]  
	out dac  

In the necklace above `in` sets the frequency of playback which is passed to osc to generate a rising ramp wave at that frequency. This 'scans' through the sample using the wav operator. Another way to look at it is that the output of osc, which repeats every 4 seconds, is 'shaped' into a sample waveform by `wav`. Any number between 0 and 1 will play the corresponding value of the sample at that point. Eg. 0.1 will play the sample at 400ms in. A continuous stream of values from `osc` will play the entire wav file. A decreasing series of numbers produced by, for example, `osc, flip` will play the sample backwards. `flip` turns the values of osc upside-down so they count down instead of up.  
Different frequencies fed to `osc` will change the speed of playback and adding a fixed number will offset the starting point of playback. So  
>
	in wavR  
	osc  
	+ 0.5  
	wav [name]  
	out dac  

will playback starting halfway through the sample. You don't need to worry if the input value exceeds 1 as it will automatically wrap around so eg. 2.37 will become 0.37 and remain in range. As the default wav length is 4 seconds, 1 second of the wav corresponds to input of 0.25, 2s to 0.5 etc. This currently still applies to wavs shorter than 4 seconds, silence will played until the 4 seconds is up. ◊  
By increasing the frequency and decreasing the amplitude of osc, a shorter section of the wav will be played.  

wavR is a pre-defined constant which gives the playback speed for a sample recorded at the same rate as the internal sample rate, which is typically 48kHz. To adjust to playback speed for samples recorded at a different rate simply `mul 44100/48000` for example, where the desired rate is 44.1kHz. This could be put more simply as `mul 441/480`, which is equivalent. To show this in a working necklace for clarity:
>
	in wavR
	mul 441/480
	osc
	wav [name]
	out dac


By design only the first 4 seconds of any wav file will be loaded. Two reasons for this are fast-loading times and to encourage creativity. You can manually edit any wav file in a free program such as Audacity to ensure the part you want to play is within the first 4 seconds, ideally starting at the beginning of the sample. Samples less then 4 seconds long are fine.

Later on, the intention is to provide more flexibility in synchronising other necklaces to wav files using `len<wavname>` signals. There is still plenty to be done in testing and possibly reworking parts of sample playback. ◊  

## Tape loop ◊  
Each listing has a tape loop available which is accessed by the `tape` operator. The tape loop is a rolling buffer of 1 second in length. The loop can be accessed by the tap operator, where the fractional part of the operand sets the delay time. For example an operand of value 2.3 will give a tap at 300 milliseconds (0.3s). Multiple `tap` operators can be used. The use of multiple `tape` operators is undefined.

## Arithmetic operations
An operand may be added in a listing of the form a/b or a\*b where a and b are valid numbers and the result is divison and multiplication respectively. For example: typing  
>
	in 2/3  
will result in  
>
	in 0.666666666...  
being added to the listing 

## Type system
This is a fancy term for inputting numerical values with a unit of measurement. If you tell someone a duration you might say: "it will take three hours", you don't just say "it will take three". In a similar way Syntə expects to be told what the number you have input means. A number can be a frequency expressed in Hertz, A bpm (beats per minute), a time in seconds or milliseconds, or a decibel level (negative numbers reduce signal level, positive increases). For example:

	440hz   <= this is the approximate frequency of the note A

	135bpm  <= a typical house music tempo

	10s     <= ten seconds, equivalent to 0.1hz

	50ms    <= 50 milliseconds, equivalent to 20hz - the lowest audible frequency

	6db     <= double the magnitude (by multiplying the signal)
	-6db    <= halve the magnitude (by multiplying the signal)

Internally all these numbers are converted to a unitless number within Syntə which is typically between zero and one, except in the case of 6db which is 2, 12db is 4 etc. The calculation to convert frequencies is:  
	input / sample rate  
And for seconds is:  
	1 / ( input \* sample rate )  

## Signals
Signals are the way of passing values around outside of the main flow through the necklace. Signals which are named and not just numbers can be referred to as registers, because they *register* a value. Usually the initial value of a named signal is 0. If you want it to begin as 1, add ' to the name. So `a` becomes `'a`. Likewise you can use the double quotation mark " to have a default value of one half. If you want to be able to overwrite the value of a signal with `out` (more than once in a necklace), which is not normally possible, you can add ^ to the name. So `a` becomes `^a`. You can use both of these special symbols with a single signal, but the circumflex ^ must come first or it will be ignored.

## CV and audio signal ranges
Varying numbers intended for output to the soundcard usually span the range [-1,+1]. CVs, or control voltages, which are used to control other parts of the signal chain and not produce sound directly typically span the range [0,1]. For instance the output of `ramp`, `saw` and `sine` which are intended to produce frequencies at audible rates (20 - 20,000Hz) all go between ±1, whereas `osc` which can be used to control a vca or the pitch of another oscillator spans between zero and 1. To make `osc` suitable for direct audio output you can use the cv2a function, which is exactly what `ramp` does. CV signals are generally slower and more rounded than audio signals. The `flip` function can be used to turn a CV upside-down, use `mul -1` to do the same for an audio signal (essentially what `saw` does). However, `flip` is much more commonly used.

## Channels ◊  
At present the output is in mono. Stereo output should be possible and is intended to be implemented in future. This will probably be via `out L` or `out R`, while `out dac` sends to both left and right channels. Please feel free to badger the author if you are keen for this to happen. 

## Synchronisation ◊  
By default (and by design) when a listing is sent to the sound engine it starts immediately. The synchronisation operators `>sync` and `\<sync` can be used to co-ordinate listings to play in time with one another. First, for a rhythmic element use the phase synchronised oscillator `posc` instead of osc. This contains a `\<sync` operation which will reset the waveform when it receives a sync pulse. The `posc` operator requires a number betweeen 0 and 1 to offset the phase, you can use 0 to start with and experiment later. For reference 0.5 would result in a phase shift of 180°, 1 would be 360° etc. To send a sync pulse you can use `>sync` to synchronise all listings containing a `posc` or `nsync` to synchronise just one. For `>sync` you can either:
>
	in -1
	.>sync

which will send one pulse. The `.` allows `.>sync` to end a listing and send it to the sound engine. The listings containing posc will remain in sync, even if you then delete this listing. Or, for periodic syncing you can:
>
	in 120bpm
	ramp
	.>sync

which will send pulses at the frequency given, in this case 1Hz. The saw function is similar to osc except the output spans between -1 and 1 which is necessary to trigger the pulse.  
Muting a sync listing will have no effect on the synchronisation, as it is send via a separate channel.  

The proposed `nsync` (not implemented) works in the same way except that it requires an operand to determine which listing to send to. Any operands given that do not correspond to the index of a listing will be wrapped, that is to say the number of listings will be subtracted until in range. For a running necklace of index 2 this will sync it once:
>
	in -1
	.nsync 2

At present the behaviour of submitting more than one instance of `>sync` to the sound engine is undefined. This may change in future. ◊  
The synchronisation is somewhat rudementary, a world away from DAW/midi sequencers, yet it has been designed to be raw and flexible in keeping with the Syntə philosophy. It also allows for a modicum of 'musicianship' as it is possible to submit listings in time with one another by hand (without sync) if you are that way inclined. Of course this is live coding which only intersects with music in general :)

## Setting levels

The `level` operator is used to adjust the audio level of a running listing, like so: `in 0.5 level 2` which would set listing two to half the available level, whcih is -6dB. This level-setting listing may be deleted straightaway and the level will persist once set. Although intended for basic mixing of listings, you may modulate the level at rates up to 1200Hz. This is an arbitary limit set to reduce clicks produced by immediate large changes in volume, while still allowing frequency modulation. You need to use `.level` to end a necklace.

## Adding functions
If you find yourself reusing the same chunk of code multiple times, it is possible to define a named function which will instantiate that chunk of code. To begin, type `[` followed by the new name. Then type the listing as normal and at the end type `]` (no operand) which will complete the function add. This function won't be saved on exit but may be used as you wish during the current session. To permanently save a function which you feel will be useful in future type `: fon` before exiting and it will be saved to the 'functions.json' file in the folder on exit from Syntə. To go back to ephemeral functions (useful for experimentation) type `: foff`.  
You may overwrite functions by typing in the same name.  
The ability to make functions like this makes the language *extensible*, which means you are able to extend the language beyond what is written in this document. One of the project aims is to build up a library of abstractions in this way to make performance easier for beginners. However there is a limit to this, as just typing 'music' and stopping there would be quite boring!  
An *abstraction* means wrapping up a bit of code into something simple to make it easier to use, for example the term 'global apartheid' is an abstraction of a system and history that involves many many processes, interconnections, organisations, trade-misinvoicing etc.

## info.go and listing.go
info.go is intended to run alongside Syntə to display useful information and error messages. The layout is as follows:
```
Syntə info *press enter to exit*                0s      <-- elapsed running time in seconds
╭───────────────────────────────────────────────────╮

                                    Load: 0.00          <-- If the sound engine is overloaded, listings will be removed without warning





                                                        <-- info and error messages will appear here






        0.00    |||||||             |                   <-- peak audio meter, approx 35dB of range, will display 'GR' if limiting takes place on the output.
      Mouse-X: 0				Mouse-Y: 0              <-- value of mouse X and Y
╰───────────────────────────────────────────────────╯
```

listing.go displays the currently running necklaces. Any that are muted will show in grey italics. The functions within a listing are 'unrolled', that is to say they are shown in terms of their atomic operations.

## Hot tips

+ use `slew 150` to smoothen a signal used for modulating volume with `mul`
+ trim samples of music to be an exact number of bars (look for repetition in the waveform of around ~2 seconds)
+ use the "!" type to use a number outside of the expected range if you need to. This is not a factorial operation
+ had a moment of inspiration and can't remember what you did? All listings accepted by the sound engine are saved by timestamp to the recordings folder in json format
+ pipe the output of `functions.go` through `less` using the `-r` flag, like this: `go run functions.go | less -r`. You can then search the contents using `/`, refer to `man less` 
+ other tips tba... ◊  

## Performing with Syntə
To prepare the audio system:
1. Set up the equipment and verify sound from Syntə is reaching the speakers
2. Turn the system down to zero
3. Type `test 2000hz`
4. Increase volume until an acceptable level
You will probably need to go above this level for perfomance once you have an audience in the room and are playing full range sounds, but this gives a good starting point that you probably don't want to go too far over. Please ensure the audience has access to hearing protection if the system is capable of more than 85dBSPL.
To specifically test the bass level and evenness try:
>
	in 10s
	osc
	mul 4
	base 2
	mul 20hz
	sino
	out dac

If the bass level fluctuates a lot at different frequencies and at different listing positions you should consider some using some bass trapping. This is beacause sound reflections between the walls will interfere and cancel out.

## Editing running listings
All currently running listings can be found in the `.temp/` folder in the root directory (main project folder). The name of the file will be the index (an non-negative integer) with the file extension .syt, eg. 0.syt , this file can be opened in any text editor. Once you have saved your edits the listing can be updated by typing `rld n` where n is the index of the listing.

## Exported signals
Up to 12 signals may be exported for input to other listings. Indicate this by capitalising the initial letter, eg. `out Env1`. This can then be used like any other signal, in the same manner as `tempo`, `pitch` and `grid`. These exported signals are daisy-chained in the same manner, so will propagate between listings in ascending order. This means that the signal will correspond to the preceeding `out`.

---

## The Sound Engine
Although it is not necessary to know how the sound engine works to perform or play with Syntə, it can be helpful to learn more about it so we'll give a brief outline here.
When a listing is send to the sound engine an internal copy is generated and added to the sequence of listings. The sound engine takes each listing in turn from 0 onwards and runs through it once to produce the value of the next sample, then it moves on to the next listing. Once it has computed all the listings the resulting samples are summed together and sent through the built-in limiter to ensure no loud surprises and the peak amplitude of the output is also sent to the info display. The resulting signal is converted to the correct format and sent to the soundcard in your computer, after which the whole process repeats.  
The terms 'listing' and 'necklace' are interchangeable. Necklace also illustrates the point that the listing is computed in a continuous loop. Although usually the result is computed from fresh each time, unless you use the `from` operator.  
All values in the sound engine are represented by 64-bit floating point numbers which have a nominal range within Syntə of between -1 and 1 inclusive. At the end of each cycle of the sound engine this is converted to a 32 bit number to be sent to the soundcard. Within a listing a value can be anywhere in the range of the 64-bit float (approx ±1.8x10^308 with an precision of about 15 decimal places.) These numbers will be limited or clipped to ±1 before audio conversion.  
The samples from each listing are added together and when there are more than four listings the sum is divided by the number of listings for unity gain. For 1, 2, 3, or 4 listings the sun is always divided by four. The result is then passed through a high-pass filter before the limiter. This is to remove any DC offsets, which means a consistent average signal other than 0, or another way to think of it is attentuating (reducing) frequencies below 4.6Hz. If my calculations are correct, any DC signal will fade below -120dB after half a second.  
The limiter reduces the level of audio above a peak value of 1 to avoid the possibility of clipping which would produce distortion. The detection algorithm of the limiter is more sensitive to higher frequencies, it expects audio to have a spectrum approximately equivalent to 'pink noise'.
The density distribution of pink noise is a good general approximation to expected frequency levels in audio (*Barrow, 1995*). The `mix` function also uses this as a guiding principle in setting a sensible level. Some adjustment may be required; however, the limiter will always kick in if internal levels are exceeded.
Because of the frequency dependent nature of the limiter detection, gain reduction may occur before the info display shows a high VU level. This is normal and you can adjust listings via the `level` operator to prevent higher frequencies from dominating the playback.
Between the limiter and the clipping stage before conversion, what is known as dither is applied to the signal. This is a tiny amount of noise to avoid rounding errors, but is probably overkill. Before the dither any envelopes associated with pausing or exiting are applied. These reduce the chances of pops or clicks.  
The whole main loop of the sound engine has a timer to produce the 'load' value that shows how much work it is doing to create each sample for the soundcard. See info display section above. If enough listings are added and the sound engine is unable to perform all calculations in time before the next sample is due, glitches or dropouts in the output can occur. In order to avoid this if a high load is detected the last listing added will be removed automatically and the sound engine restarted.  

---

### A note on licensing
The work in this file and all others in this repository is now licenced. See the licence.md file for details.  

**TL;DR**  

+ Running the software in person (not via a network) for the purposes of performance or education won't lead to any negative consequences.  

+ Using, modifying, or distributing the software in binary or source code form for commercial purposes is not permitted.  

This semi-permissive licence has been chosen to reflect the fact that this implementation is a prototype only and has no commercial value.  
Suggestions, including improvements to the code are welcome.  
If you would like to fork the code for more radical changes then we wholeheartly suggest you instead write a unique live coding platform from scratch. The file is of an order of only two thousand lines of code after all, and the learning experience will be invaluable to you.   
Please get in touch if you have further questions.  

### Influences and other live-coding environments
The term live-coding is generally taken to mean on-the-fly composition of algorithmically generated audio or graphical artifacts using software.  
We recommend exploring TidalCycles and Pure Data in particular. If you are more visual/graphical oriented there are live coding languages/environments for that space too.  
Many influences and inspirations have been drawn upon either deliberately or subconciously in the creation of Syntə. They include assembly language, Forth, RPN calculators, VIM, the live-coding laguages mentioned above, modular synthesis, Musique concrète, Go itself (the language Syntə is implemented in) and unix-like systems in general - primarily FreeBSD which was the first OS to host Syntə, both for development and execution.  

### Future possiblities for Syntə  
The main focus of development is building up a library of functions which provide useful and intuitive abstractions to speed up and simplify performing.  
Work is being done to improve the rate of composition available, by enabling easier editing of listings and more modulation between them. Current roadmap includes: automated testing of the examples given in this documenti; simplification of the input parsing and compilation section of the code to improve readability and operation; further changes to the implementation of tape and reel operators; improvements to the usability of the wav operator to facilitate tempo calculations. ◊  
It would be great if translations of this document and the language itself become available in future, get in touch if you can help with this. A live distro with Syntə included could be useful for users of other operating systems to boot from. Another possiblity for the code is integration with a terminal library such as tcell or bubbletea for a slicker UI. Also, the Sound Engine could be adapted to a 'headless' mode for use with an editor such as Atom. This option hasn't been pursued for now as it is helpful to provide immediate feedback via info.go with each operation entered. Future additions include providing for audio input and use of other sound drivers than OSS/ALSA. A browser-based interface is another option, which would make the program extremely portable. This could be as simple as redirecting stdout to a local websocket server.
You may consider the current implementation of Syntə to be a prototype. At present most of the basic features have been taken care of, and as the language is tried out by different people, changes and adjustments that will tidy up and make performing easier may become apparent. The core aims of fun and accessibility won't change. 

### General principles of live coding performance  
These are, as the author understands it (opinions may differ):  

+ Display your code to the audience  

+ Prefer fresh code over copy-pasta  

+ Improvisation and exploration are key  

+ An accessible and welcoming space is essential  

+ Share and mentor where possible  

+ Mistakes and errors are embraced and honoured  
 

**To do: (for this document)** ◊  

>how to modulate pitch and volume, including ratios and exponents  
feedback and what it means  
filtering  
delay and reverb  
pause/play  
mouse control  
tempo and pitch (not fully implemented yet)  
euclidian rhythms  
fractal synthesis (not implemented yet)  
finite recursion (not implemented yet)  
mute and solo

---

[Back to top](#top)
