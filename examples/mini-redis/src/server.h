#ifndef SERVER_H
#define SERVER_H

#include <signal.h>

extern volatile sig_atomic_t g_shutdown;

int start_server(int port);

#endif
