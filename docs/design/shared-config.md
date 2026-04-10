# Design: Shared / Inherited Cloche Configuration

## Original Feedback

> As a "large" organization, I have several projects that all follow
> the exact same workflow, but with some minor changes that capture
> project specific configs. Currently I need to copy cloche config
> files around to each of these projects. What if I discover a bug
> in one of my cloche workflows? I'll have to carefully propagate
> changes to other projects and make sure I don't overwrite project
> specific configs. Is there a way to separate project-agnostic
> config from project-specific config? I mean I could do that
> manually, but would it make sense to have some functionality built
> into cloche specifically for that? It'd be good to have a clear
> path for propagating initial configuration to projects and to
> propagate changes.
