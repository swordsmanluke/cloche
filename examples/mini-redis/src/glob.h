#ifndef GLOB_H
#define GLOB_H

#include <stdbool.h>
#include <stddef.h>

bool glob_match(const char *pattern, size_t plen,
                const char *str, size_t slen);

#endif
