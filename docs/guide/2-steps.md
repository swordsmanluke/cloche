# Steps: the basic building block

A Step does one job and reports one Result. A Result is the enum that declares what should happens next.

![The test step reports success and goes to done, or reports fail and goes to fix.](img/step.svg)

Defining a Step and its allowed Results in the DSL:

```
# Steps are declared with a name, an instruction key (see below), and their valid results.
step test {
  run     = "make test"
  results = [success, fail]
}

# Then build your workflow by declaring where each Result should lead
test:success -> done
test:fail    -> fix
```

Existing software can't use cloche-style results, so cloche considers exit code 0 as an implicit 'success' and any other as 'fail'.
In this case, when `make test` exits 0, the `test` Step follows the `success` edge and the run is `done`. Any other exit code 
reports `fail`, and cloche will run `fix` next.

## Results

A Result is just a string value that tells cloche what edge in the workflow graph a Step wants to follow next.

If you think of a Step as a shell call, the Result is akin to an exit code.

### Basic Results

By convention, all Steps are presumed to have `success` and `fail` Results.

And since existing software can't send cloche-style Results, in the absence of a specified Result, cloche treates exit code 0 as `success`
and any other exit code as `fail`.

Thus, you can use most existing shell programs with cloche without requiring any special handling.

### Extended Results

Sometimes you'll want to branch on more than basic success / fail status though - so you can define any set of Result codes that 
you like for your Steps.

To select a Result, a Step just needs to print the result to stdout, in a specific format: `CLOCHE_RESULT:<step code>:<result>`  

The `step code` is a nonce value cloche uses to ensure that mixed output (for instance, when reading from a previous step's output) 
doesn't interfere with the current step. And `result` is of course, the desired edge to follow.

## Basic Steps

Step types are defined by behaviors. You select the behavior by setting an instruction Key.

The Script and Agent Steps are the main structure of most workflows. You'll use these the most.

### Script

**Key:** `run`

Execute a shell command. Run tests, git commands, or execute your own custom code. Anything that can be run from a non-interactive shell 
can be run here. 

```
step test {
  run = "path_to_script"
}
```

### Agent

**Key:** `prompt`

Execute a prompt file using the configured LLM agent. Use this where you need an LLM's flexibility.

```
step code
  prompt = "path_to_prompt_file"
}
```

## Advanced Steps

Scripts and Agents are powerful and account for 90% of all Workflow Steps - but there's two more Step types to handle some specific cases:

### Poll
**Key:** `poll`

Runs a script periodically until a condition is met, then proceeds. 

This is a way to put a Workflow on hold while waiting for an external process or condition to be met. For instance, waiting for
feedback on a PR, or for a certain time of day before proceeding. Since active tasks hold a "slot" in cloche, a poll step provides
a way to release a slot while a Workflow is waiting. Once the script's check passes, the Workflow can resume.

See: [Poll Steps](http://cloche.perfectlunacy.com/concepts/#poll-steps) for details.

### Workflow

**Key:** `workflow_name`

Trigger another cloche Workflow and wait for its Result. Use this to handoff control across the host/container boundary 
(see Workflows, next doc!), to segment responsibilities, or to share reuseable behavior across multiple places.

If a Step is kind of like a function, a Workflow is like a Class. Bundle up a bit of functionality in a single Workflow and
now you can refer to it by name.

When you build your first couple Workflows, you probably wont need or particularly want Workflow steps. But trust me, as your
Workflow complexity grows, you'll get to a point where you'll be glad you have them.

---
**Next: [Workflows](3-workflows.md) →**
