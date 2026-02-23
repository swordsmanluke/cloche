CC       = gcc
CFLAGS   = -Wall -Wextra -Werror -pedantic -std=c11 -g -D_POSIX_C_SOURCE=200809L -D_DEFAULT_SOURCE
SRC_DIR  = src
TEST_DIR = test
BUILD_DIR = build

SRCS     = $(wildcard $(SRC_DIR)/*.c)
OBJS     = $(SRCS:$(SRC_DIR)/%.c=$(BUILD_DIR)/%.o)

TEST_SRCS = $(wildcard $(TEST_DIR)/*.c)
LIB_OBJS  = $(filter-out $(BUILD_DIR)/server.o, $(OBJS))
TEST_OBJS = $(TEST_SRCS:$(TEST_DIR)/%.c=$(BUILD_DIR)/test/%.o)

all: mini-redis

mini-redis: $(OBJS)
	$(CC) $(CFLAGS) -o $@ $^

$(BUILD_DIR)/%.o: $(SRC_DIR)/%.c | $(BUILD_DIR)
	$(CC) $(CFLAGS) -c $< -o $@

$(BUILD_DIR)/test/%.o: $(TEST_DIR)/%.c | $(BUILD_DIR)/test
	$(CC) $(CFLAGS) -I$(SRC_DIR) -c $< -o $@

$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)

$(BUILD_DIR)/test:
	mkdir -p $(BUILD_DIR)/test

test_bin: mini-redis $(LIB_OBJS) $(TEST_OBJS)
	$(CC) $(CFLAGS) -o test_runner $(LIB_OBJS) $(TEST_OBJS)

test: test_bin
	./test_runner

clean:
	rm -rf $(BUILD_DIR) mini-redis test_runner

.PHONY: all test test_bin clean
