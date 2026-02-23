#include "test.h"

extern test_case_t resp_tests[];
extern int resp_test_count;
extern test_case_t hashtable_tests[];
extern int hashtable_test_count;
extern test_case_t glob_tests[];
extern int glob_test_count;
extern test_case_t ttl_tests[];
extern int ttl_test_count;
extern int run_integration_tests(void);

int main(void) {
    setbuf(stdout, NULL);
    int total_failed = 0;

    total_failed += run_test_suite("RESP Parser Tests",
                                   resp_tests, resp_test_count);
    total_failed += run_test_suite("Hash Table Tests",
                                   hashtable_tests, hashtable_test_count);
    total_failed += run_test_suite("Glob Pattern Tests",
                                   glob_tests, glob_test_count);
    total_failed += run_test_suite("TTL/Expiration Tests",
                                   ttl_tests, ttl_test_count);
    total_failed += run_integration_tests();

    printf("\n");
    if (total_failed == 0) {
        printf("All tests passed!\n");
    } else {
        printf("%d test(s) FAILED\n", total_failed);
    }

    return total_failed == 0 ? 0 : 1;
}
