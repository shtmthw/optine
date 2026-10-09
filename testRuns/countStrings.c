#include <stdio.h>

int main() {
    char text[] = "This is a sample text to count individual strings.";
    int count = 0;

    for (int i = 0; text[i] != '\0'; i++) {
        if (text[i] == ' ' || text[i] == '\n') {
            count++;
        }
    }

    printf("Number of individual strings: %d\n", count);
    return 0;
}