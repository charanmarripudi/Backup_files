#include <SDL2/SDL.h>
#include <stdio.h>

int main(int argc, char* argv[]) {
    printf("SDL2 version: %d.%d.%d\n", 
        SDL_MAJOR_VERSION, SDL_MINOR_VERSION, SDL_PATCHLEVEL);
    
    if (SDL_Init(SDL_INIT_VIDEO) != 0) {
        printf("SDL_Init failed: %s\n", SDL_GetError());
        return 1;
    }
    printf("SDL_Init succeeded\n");
    
    // Try to create a window
    SDL_Window* window = SDL_CreateWindow("Test", 
        SDL_WINDOWPOS_UNDEFINED, SDL_WINDOWPOS_UNDEFINED,
        640, 480, SDL_WINDOW_SHOWN);
    
    if (!window) {
        printf("Window creation failed: %s\n", SDL_GetError());
        SDL_Quit();
        return 1;
    }
    printf("Window created successfully\n");
    
    // Keep window open for 2 seconds
    SDL_Delay(2000);
    
    SDL_DestroyWindow(window);
    SDL_Quit();
    return 0;
}
