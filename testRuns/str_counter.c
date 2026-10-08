#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int main(void) {
    char *line = NULL;
    size_t len = 0;
    printf("Enter a string: ");
    if (getline(&line, &len, stdin) == -1) {
        perror("getline");
        return EXIT_FAILURE;
    }
    /* Remove trailing newline if present */
    line[strcspn(line, "\n")] = '\0';
    size_t bytes = strlen(line);
    size_t bits = bytes * 8;
    printf("String: \"%s\"\n", line);
    printf("Bytes: %zu\n", bytes);
    printf("Bits: %zu\n", bits);
    free(line);
    return EXIT_SUCCESS;
}
