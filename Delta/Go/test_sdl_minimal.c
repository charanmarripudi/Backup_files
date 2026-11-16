#include <SDL2/SDL.h>
#include <stdio.h>

int main() {
    printf("Testing SDL2 initialization...\n");
    
    if (SDL_Init(SDL_INIT_VIDEO) < 0) {
        printf("SDL_Init Error: %s\n", SDL_GetError());
        return 1;
    }
    
    printf("SDL initialized successfully!\n");
    printf("Current video driver: %s\n", SDL_GetCurrentVideoDriver());
    
    printf("Creating window...\n");
    SDL_Window *window = SDL_CreateWindow("SDL Test",
                                          SDL_WINDOWPOS_UNDEFINED,
                                          SDL_WINDOWPOS_UNDEFINED,
                                          640, 480,
                                          SDL_WINDOW_SHOWN);
    
    if (!window) {
        printf("Window creation failed: %s\n", SDL_GetError());
        SDL_Quit();
        return 1;
    }
    
    printf("Window created! Keeping open for 3 seconds...\n");
    SDL_Delay(3000);
    
    SDL_DestroyWindow(window);
    SDL_Quit();
    printf("Test passed!\n");
    return 0;
}
