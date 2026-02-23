#include "glob.h"

static bool match_char_class(const char *pattern, size_t plen,
                             size_t *pi, char c) {
    size_t i = *pi;
    /* skip past '[' */
    i++;
    if (i >= plen) {
        return false;
    }

    bool negate = false;
    if (pattern[i] == '!' || pattern[i] == '^') {
        negate = true;
        i++;
    }

    bool matched = false;
    bool first = true;

    while (i < plen && (pattern[i] != ']' || first)) {
        first = false;
        char lo = pattern[i];
        char hi = lo;

        if (i + 2 < plen && pattern[i + 1] == '-' && pattern[i + 2] != ']') {
            hi = pattern[i + 2];
            i += 3;
        } else {
            i++;
        }

        if ((unsigned char)c >= (unsigned char)lo &&
            (unsigned char)c <= (unsigned char)hi) {
            matched = true;
        }
    }

    if (i >= plen) {
        /* no closing ']' found */
        return false;
    }

    /* skip past ']' */
    i++;
    *pi = i;

    return negate ? !matched : matched;
}

bool glob_match(const char *pattern, size_t plen,
                const char *str, size_t slen) {
    size_t pi = 0, si = 0;
    size_t star_pi = (size_t)-1;
    size_t star_si = (size_t)-1;

    while (si < slen) {
        if (pi < plen && pattern[pi] == '*') {
            star_pi = pi;
            star_si = si;
            pi++;
            continue;
        }
        if (pi < plen && (pattern[pi] == '?' ||
                          pattern[pi] == str[si])) {
            pi++;
            si++;
            continue;
        }
        if (pi < plen && pattern[pi] == '[') {
            size_t saved_pi = pi;
            if (match_char_class(pattern, plen, &pi, str[si])) {
                si++;
                continue;
            }
            pi = saved_pi;
        }
        /* mismatch: backtrack to last '*' */
        if (star_pi != (size_t)-1) {
            pi = star_pi + 1;
            star_si++;
            si = star_si;
            continue;
        }
        return false;
    }

    /* consume trailing '*' in pattern */
    while (pi < plen && pattern[pi] == '*') {
        pi++;
    }
    return pi == plen;
}
