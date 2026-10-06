#include <stdio.h>
#include <stdlib.h>
#include <ctype.h>

#define BOARD_SIZE 3

void print_board(char board[BOARD_SIZE][BOARD_SIZE]) {
    printf("\n   0   1   2\n");
    for (int i = 0; i < BOARD_SIZE; i++) {
        printf("%d  ", i);
        for (int j = 0; j < BOARD_SIZE; j++) {
            printf(" %c ", board[i][j]);
            if (j < BOARD_SIZE - 1) printf("|");
        }
        printf("\n");
        if (i < BOARD_SIZE - 1) {
            printf("   ---------\n");
        }
    }
    printf("\n");
}

int check_winner(char board[BOARD_SIZE][BOARD_SIZE]) {
    // Check rows
    for (int i = 0; i < BOARD_SIZE; i++) {
        if (board[i][0] != ' ' && board[i][0] == board[i][1] && board[i][1] == board[i][2])
            return board[i][0];
    }
    // Check columns
    for (int j = 0; j < BOARD_SIZE; j++) {
        if (board[0][j] != ' ' && board[0][j] == board[1][j] && board[1][j] == board[2][j])
            return board[0][j];
    }
    // Diagonals
    if (board[0][0] != ' ' && board[0][0] == board[1][1] && board[1][1] == board[2][2])
        return board[0][0];
    if (board[0][2] != ' ' && board[0][2] == board[1][1] && board[1][1] == board[2][0])
        return board[0][2];
    // Check for draw
    int empty = 0;
    for (int i = 0; i < BOARD_SIZE; i++)
        for (int j = 0; j < BOARD_SIZE; j++)
            if (board[i][j] == ' ') empty++;
    if (empty == 0) return 'D'; // draw
    return ' '; // game continues
}

int main(void) {
    char board[BOARD_SIZE][BOARD_SIZE];
    for (int i = 0; i < BOARD_SIZE; i++)
        for (int j = 0; j < BOARD_SIZE; j++)
            board[i][j] = ' ';
    char current = 'X';
    while (1) {
        print_board(board);
        int row, col;
        printf("Player %c, enter row and column (e.g., 0 2): ", current);
        if (scanf("%d %d", &row, &col) != 2) {
            printf("Invalid input. Try again.\n");
            // clear stdin
            int c;
            while ((c = getchar()) != '\n' && c != EOF) ;
            continue;
        }
        if (row < 0 || row >= BOARD_SIZE || col < 0 || col >= BOARD_SIZE) {
            printf("Position out of bounds. Try again.\n");
            continue;
        }
        if (board[row][col] != ' ') {
            printf("Cell already occupied. Try again.\n");
            continue;
        }
        board[row][col] = current;
        int result = check_winner(board);
        if (result == 'X' || result == 'O') {
            print_board(board);
            printf("Player %c wins!\n", result);
            break;
        } else if (result == 'D') {
            print_board(board);
            printf("It's a draw!\n");
            break;
        }
        current = (current == 'X') ? 'O' : 'X';
    }
    return 0;
}
