#ifndef COMMANDS_H
#define COMMANDS_H

#include "client.h"
#include "hashtable.h"
#include "resp.h"

typedef void (*cmd_handler_t)(client_t *client, hashtable_t *store,
                              resp_value_t *args, int argc);

void dispatch_command(client_t *client, hashtable_t *store,
                      resp_value_t *cmd);

#endif
