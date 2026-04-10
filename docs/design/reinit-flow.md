# Design: Re-initialization Flow (`cloche init`)

## Original Feedback

> Copying .cloche files from another directory and running cloche init
> does not work - I get ".cloche/develop.cloche already exists".
> This would prevent someone from checking out a copy of a repo
> that's already been setup with cloche and getting their instance
> of cloche working on it.

### Notes

- The re-initialization flow needs defining
- `cloche init` should only set up bare minimum; move the bells and
  whistles to a command switch.
